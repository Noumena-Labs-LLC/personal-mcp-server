package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

const finishedJobRetention = time.Hour

type commandJob struct {
	mu              sync.Mutex
	ID              string
	Name            string
	Cwd             string
	Started         time.Time
	Finished        time.Time
	Status          string
	Result          map[string]any
	Err             string
	Stdout          limitBuffer
	Stderr          limitBuffer
	CancelRequested bool
	cancel          context.CancelFunc
}

type StartNamedResult struct {
	JobID     string `json:"job_id"`
	Name      string `json:"name"`
	Cwd       string `json:"cwd,omitempty"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

type JobArgs struct {
	JobID string `json:"job_id"`
}

type JobReadArgs struct {
	JobID     string `json:"job_id"`
	TailBytes int    `json:"tail_bytes"`
}

type JobListArgs struct {
	Cwd             string `json:"cwd"`
	IncludeFinished bool   `json:"include_finished"`
}

func (r *Runner) StartNamed(raw json.RawMessage) (any, error) {
	prepared, err := r.prepareNamedCommand(raw)
	if err != nil {
		return nil, err
	}
	r.pruneFinishedJobs(time.Now())
	jobID := "job_" + randomHex(12)
	ctx, cancel := context.WithCancel(context.Background())
	job := &commandJob{
		ID:      jobID,
		Name:    prepared.Args.Name,
		Cwd:     prepared.Cwd,
		Started: time.Now().UTC(),
		Status:  "running",
		Result:  map[string]any{},
		cancel:  cancel,
	}
	job.Stdout.Limit = r.Cfg.Limits.MaxCommandOutputBytes
	job.Stderr.Limit = r.Cfg.Limits.MaxCommandOutputBytes
	for k, v := range prepared.Extra {
		job.Result[k] = v
	}
	configuredRunMode := commandRunMode(prepared.Spec)
	job.Result["run_mode"] = "background_exec"
	job.Result["configured_run_mode"] = configuredRunMode
	job.Result["note"] = "background jobs always use background_exec regardless of project run_mode"
	if prepared.ProjectState.Found && prepared.Source == "project" {
		job.Result["project"] = map[string]any{"root": prepared.ProjectState.Root, "trusted": prepared.ProjectState.Trusted}
	}

	r.jobMu.Lock()
	if r.jobs == nil {
		r.jobs = map[string]*commandJob{}
	}
	r.jobs[jobID] = job
	r.jobMu.Unlock()

	go r.runCommandJob(ctx, job, prepared.Spec, prepared.Cwd, prepared.FinalArgs)

	return StartNamedResult{JobID: job.ID, Name: job.Name, Cwd: job.Cwd, Status: job.Status, StartedAt: job.Started.Format(time.RFC3339)}, nil
}

func (r *Runner) runCommandJob(parentCtx context.Context, job *commandJob, spec config.CommandSpec, cwd string, args []string) {
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(r.Cfg.Limits.CommandTimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.Exec, args...)
	cmd.Dir = cwd
	cmd.Env = buildEnvFromParts(spec.Env, spec.EnvFromHost)
	cmd.Stdout = jobOutputWriter{runner: r, job: job, stream: "stdout"}
	cmd.Stderr = jobOutputWriter{runner: r, job: job, stream: "stderr"}
	setProcessGroup(cmd)

	started := time.Now()
	startErr := cmd.Start()
	if startErr != nil {
		r.finishCommandJob(job, "failed", startErr.Error(), map[string]any{"exit_code": -1, "timed_out": false, "duration_ms": time.Since(started).Milliseconds()})
		return
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	timedOut := false
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		timedOut = true
		killProcessTree(cmd)
		waitErr = <-done
	}

	wasCancelled := false
	job.mu.Lock()
	wasCancelled = job.CancelRequested || job.Status == "cancelling"
	job.mu.Unlock()

	exitCode := 0
	if waitErr != nil {
		exitCode = -1
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		}
	}
	status := "exited"
	errText := ""
	if waitErr != nil && exitCode != 0 {
		errText = waitErr.Error()
	}
	ctxErr := ctx.Err()
	if wasCancelled || errors.Is(parentCtx.Err(), context.Canceled) || errors.Is(ctxErr, context.Canceled) || errors.Is(waitErr, context.Canceled) {
		status = "cancelled"
		timedOut = false
		if ctxErr != nil {
			errText = ctxErr.Error()
		} else {
			errText = context.Canceled.Error()
		}
	} else if timedOut || errors.Is(ctxErr, context.DeadlineExceeded) {
		status = "timed_out"
		timedOut = true
		if ctxErr != nil {
			errText = ctxErr.Error()
		} else {
			errText = context.DeadlineExceeded.Error()
		}
	}
	r.finishCommandJob(job, status, errText, map[string]any{"exit_code": exitCode, "timed_out": timedOut, "duration_ms": time.Since(started).Milliseconds()})
}

func setProcessGroup(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

func (r *Runner) finishCommandJob(job *commandJob, status, errText string, result map[string]any) {
	now := time.Now().UTC()
	job.mu.Lock()
	if job.CancelRequested && (status == "failed" || status == "timed_out") {
		status = "cancelled"
		result["timed_out"] = false
	}
	if status == "failed" && errText == context.Canceled.Error() {
		status = "cancelled"
		result["timed_out"] = false
	}
	for k, v := range result {
		job.Result[k] = v
	}
	job.Result["stdout"] = job.Stdout.String()
	job.Result["stderr"] = job.Stderr.String()
	job.Result["stdout_truncated"] = job.Stdout.Truncated
	job.Result["stderr_truncated"] = job.Stderr.Truncated
	job.Finished = now
	job.Status = status
	job.Err = errText
	job.mu.Unlock()
}

type jobOutputWriter struct {
	runner *Runner
	job    *commandJob
	stream string
}

func (w jobOutputWriter) Write(p []byte) (int, error) {
	w.job.mu.Lock()
	defer w.job.mu.Unlock()
	if w.stream == "stderr" {
		if w.job.Stderr.Limit == 0 {
			w.job.Stderr.Limit = w.runner.Cfg.Limits.MaxCommandOutputBytes
		}
		return w.job.Stderr.Write(p)
	}
	if w.job.Stdout.Limit == 0 {
		w.job.Stdout.Limit = w.runner.Cfg.Limits.MaxCommandOutputBytes
	}
	return w.job.Stdout.Write(p)
}

func (r *Runner) JobStatus(raw json.RawMessage) (any, error) {
	jobID, err := parseJobID(raw)
	if err != nil {
		return nil, err
	}
	r.jobMu.Lock()
	job := r.jobs[jobID]
	r.jobMu.Unlock()
	if job == nil {
		return nil, fmt.Errorf("unknown job_id %q", jobID)
	}
	return job.snapshot(false, 0), nil
}

func (r *Runner) JobRead(raw json.RawMessage) (any, error) {
	var a JobReadArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.TailBytes <= 0 {
		a.TailBytes = 12000
	}
	r.jobMu.Lock()
	job := r.jobs[a.JobID]
	r.jobMu.Unlock()
	if job == nil {
		return nil, fmt.Errorf("unknown job_id %q", a.JobID)
	}
	return job.snapshot(true, a.TailBytes), nil
}

func (r *Runner) JobCancel(raw json.RawMessage) (any, error) {
	jobID, err := parseJobID(raw)
	if err != nil {
		return nil, err
	}
	r.jobMu.Lock()
	job := r.jobs[jobID]
	r.jobMu.Unlock()
	if job == nil {
		return nil, fmt.Errorf("unknown job_id %q", jobID)
	}
	job.mu.Lock()
	alreadyDone := !job.Finished.IsZero()
	var cancel context.CancelFunc
	if !alreadyDone {
		job.CancelRequested = true
		job.Status = "cancelling"
		cancel = job.cancel
	}
	status := job.Status
	job.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return map[string]any{"job_id": jobID, "status": status, "already_finished": alreadyDone}, nil
}

func (r *Runner) JobList(raw json.RawMessage) (any, error) {
	var a JobListArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
	}
	r.pruneFinishedJobs(time.Now())
	r.jobMu.Lock()
	jobs := make([]*commandJob, 0, len(r.jobs))
	for _, job := range r.jobs {
		jobs = append(jobs, job)
	}
	r.jobMu.Unlock()
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		snapshot := job.snapshot(false, 0)
		if !a.IncludeFinished {
			if _, ok := snapshot["finished_at"]; ok {
				continue
			}
		}
		if strings.TrimSpace(a.Cwd) != "" && snapshot["cwd"] != a.Cwd {
			continue
		}
		items = append(items, snapshot)
	}
	return map[string]any{"jobs": items, "count": len(items), "retention_seconds": int(finishedJobRetention.Seconds())}, nil
}

func parseJobID(raw json.RawMessage) (string, error) {
	var a JobArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.JobID) == "" {
		return "", errors.New("job_id is required")
	}
	return a.JobID, nil
}

func (r *Runner) pruneFinishedJobs(now time.Time) {
	r.jobMu.Lock()
	defer r.jobMu.Unlock()
	for id, job := range r.jobs {
		job.mu.Lock()
		expired := !job.Finished.IsZero() && now.Sub(job.Finished) > finishedJobRetention
		job.mu.Unlock()
		if expired {
			delete(r.jobs, id)
		}
	}
}

func (j *commandJob) snapshot(includeOutput bool, tailBytes int) map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]any{
		"job_id":     j.ID,
		"name":       j.Name,
		"cwd":        j.Cwd,
		"status":     j.Status,
		"started_at": j.Started.Format(time.RFC3339),
	}
	if !j.Finished.IsZero() {
		out["finished_at"] = j.Finished.Format(time.RFC3339)
		out["duration_ms"] = j.Finished.Sub(j.Started).Milliseconds()
	} else {
		out["duration_ms"] = time.Since(j.Started).Milliseconds()
	}
	if j.Err != "" {
		out["error"] = j.Err
	}
	if j.Result != nil {
		for _, key := range []string{"exit_code", "timed_out", "timeout_phase", "run_mode", "configured_run_mode", "note"} {
			if value, ok := j.Result[key]; ok {
				out[key] = value
			}
		}
	}
	if includeOutput {
		stdout := j.Stdout.String()
		stderr := j.Stderr.String()
		if j.Result != nil {
			if value, ok := j.Result["stdout"].(string); ok && stdout == "" {
				stdout = value
			}
			if value, ok := j.Result["stderr"].(string); ok && stderr == "" {
				stderr = value
			}
		}
		out["stdout_tail"] = tailString(stdout, tailBytes)
		out["stderr_tail"] = tailString(stderr, tailBytes)
		out["stdout_truncated"] = j.Stdout.Truncated || len(stdout) > tailBytes
		out["stderr_truncated"] = j.Stderr.Truncated || len(stderr) > tailBytes
		out["output_available"] = stdout != "" || stderr != "" || j.Result != nil
	}
	return out
}

func tailString(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	return s[len(s)-maxBytes:]
}
