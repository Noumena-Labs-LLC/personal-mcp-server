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

const (
	finishedJobRetention      = time.Hour
	defaultJobOutputLines     = 2000
	defaultJobOutputTailLines = 200
	jobOutputChannelSize      = 64
	defaultMaxBackgroundJobs  = 16
)

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
	Stdout          *jobOutputStream
	Stderr          *jobOutputStream
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
	TailLines int    `json:"tail_lines"`
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
	limits := r.jobOutputLimits()
	job := &commandJob{
		ID:      jobID,
		Name:    prepared.Args.Name,
		Cwd:     prepared.Cwd,
		Started: time.Now().UTC(),
		Status:  "running",
		Result:  map[string]any{},
		Stdout:  newJobOutputStream(limits),
		Stderr:  newJobOutputStream(limits),
		cancel:  cancel,
	}
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

	active, maxJobs, registered := r.registerJob(job)
	if !registered {
		cancel()
		job.closeOutputStreams()
		return backgroundJobsBusyResult(prepared.Args.Name, prepared.Cwd, active, maxJobs), nil
	}

	go r.runCommandJob(ctx, job, prepared.Spec, prepared.Cwd, prepared.FinalArgs)

	return job.startResult(), nil
}

func (r *Runner) maxBackgroundJobs() int {
	return defaultMaxBackgroundJobs
}

func backgroundJobsBusyResult(name, cwd string, active, maxJobs int) map[string]any {
	return map[string]any{
		"ok":                  false,
		"busy":                true,
		"retryable":           true,
		"error":               "too_many_background_jobs",
		"message":             "too many background jobs are active; wait for one to finish or cancel an existing job",
		"name":                name,
		"cwd":                 cwd,
		"active_jobs":         active,
		"max_background_jobs": maxJobs,
	}
}

func (r *Runner) jobOutputLimits() jobOutputLimits {
	maxBytes := int(r.Cfg.Limits.MaxCommandOutputBytes)
	if maxBytes <= 0 {
		maxBytes = 65536
	}
	return jobOutputLimits{
		MaxLines:     defaultJobOutputLines,
		MaxLineBytes: maxBytes,
		MaxTailBytes: maxBytes,
		ChannelSize:  jobOutputChannelSize,
	}
}

func (r *Runner) runCommandJob(parentCtx context.Context, job *commandJob, spec config.CommandSpec, cwd string, args []string) {
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(r.Cfg.Limits.CommandTimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.Exec, args...)
	cmd.Dir = cwd
	cmd.Env = buildEnvFromParts(spec.Env, spec.EnvFromHost)
	cmd.Stdout = jobOutputWriter{stream: job.Stdout}
	cmd.Stderr = jobOutputWriter{stream: job.Stderr}
	setProcessGroup(cmd)

	started := time.Now()
	startErr := cmd.Start()
	if startErr != nil {
		job.closeOutputStreams()
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
	job.closeOutputStreams()

	wasCancelled := job.cancelRequested()

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

func (r *Runner) registerJob(job *commandJob) (active, maxJobs int, registered bool) {
	r.withJobRegistryLock(func() {
		if r.jobs == nil {
			r.jobs = map[string]*commandJob{}
		}
		active = r.activeJobs
		maxJobs = r.maxBackgroundJobs()
		if maxJobs > 0 && active >= maxJobs {
			return
		}
		r.jobs[job.ID] = job
		r.activeJobs++
		active = r.activeJobs
		registered = true
	})
	return active, maxJobs, registered
}

func (r *Runner) lookupJob(jobID string) *commandJob {
	var job *commandJob
	r.withJobRegistryLock(func() {
		job = r.jobs[jobID]
	})
	return job
}

func (r *Runner) snapshotJobs() []*commandJob {
	var jobs []*commandJob
	r.withJobRegistryLock(func() {
		jobs = make([]*commandJob, 0, len(r.jobs))
		for _, job := range r.jobs {
			jobs = append(jobs, job)
		}
	})
	return jobs
}

func (r *Runner) removeFinishedJob(job *commandJob) {
	if job == nil {
		return
	}
	r.withJobRegistryLock(func() {
		if r.activeJobs > 0 {
			r.activeJobs--
		}
	})
}

func (r *Runner) removeExpiredJobs(expired map[string]*commandJob) {
	if len(expired) == 0 {
		return
	}
	r.withJobRegistryLock(func() {
		for id, job := range expired {
			if r.jobs[id] == job {
				delete(r.jobs, id)
			}
		}
	})
}

func (r *Runner) withJobRegistryLock(fn func()) {
	r.jobMu.Lock()
	defer r.jobMu.Unlock()
	fn()
}

func (j *commandJob) closeOutputStreams() {
	if j.Stdout != nil {
		j.Stdout.CloseAndFlush()
	}
	if j.Stderr != nil {
		j.Stderr.CloseAndFlush()
	}
}

func setProcessGroup(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

func (r *Runner) finishCommandJob(job *commandJob, status, errText string, result map[string]any) {
	job.finish(status, errText, result)
	r.removeFinishedJob(job)
}

type jobOutputWriter struct {
	stream *jobOutputStream
}

func (w jobOutputWriter) Write(p []byte) (int, error) {
	if w.stream == nil {
		return len(p), nil
	}
	return w.stream.Write(p)
}

func (r *Runner) JobStatus(raw json.RawMessage) (any, error) {
	jobID, err := parseJobID(raw)
	if err != nil {
		return nil, err
	}
	job := r.lookupJob(jobID)
	if job == nil {
		return nil, fmt.Errorf("unknown job_id %q", jobID)
	}
	return job.snapshot(false, defaultJobOutputTailLines), nil
}

func (r *Runner) JobRead(raw json.RawMessage) (any, error) {
	var a JobReadArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.TailLines <= 0 {
		a.TailLines = defaultJobOutputTailLines
	}
	job := r.lookupJob(a.JobID)
	if job == nil {
		return nil, fmt.Errorf("unknown job_id %q", a.JobID)
	}
	return job.snapshot(true, a.TailLines), nil
}

func (r *Runner) JobCancel(raw json.RawMessage) (any, error) {
	jobID, err := parseJobID(raw)
	if err != nil {
		return nil, err
	}
	job := r.lookupJob(jobID)
	if job == nil {
		return nil, fmt.Errorf("unknown job_id %q", jobID)
	}
	status, alreadyDone, cancel := job.requestCancel()
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
	jobs := r.snapshotJobs()
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		snapshot := job.snapshot(false, defaultJobOutputTailLines)
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
	jobs := r.snapshotJobs()

	expired := make(map[string]*commandJob)
	for _, job := range jobs {
		if job == nil {
			continue
		}
		if job.isExpired(now) {
			expired[job.ID] = job
		}
	}
	r.removeExpiredJobs(expired)
}

func (j *commandJob) snapshot(includeOutput bool, tailLines int) map[string]any {
	out := map[string]any{}
	j.withLock(func() {
		out["job_id"] = j.ID
		out["name"] = j.Name
		out["cwd"] = j.Cwd
		out["status"] = j.Status
		out["started_at"] = j.Started.Format(time.RFC3339)
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
	})

	if includeOutput {
		if tailLines <= 0 {
			tailLines = defaultJobOutputTailLines
		}
		stdoutTail := outputTailResult{TailLines: tailLines}
		stderrTail := outputTailResult{TailLines: tailLines}
		if j.Stdout != nil {
			stdoutTail = j.Stdout.Tail(tailLines)
		}
		if j.Stderr != nil {
			stderrTail = j.Stderr.Tail(tailLines)
		}
		out["tail_mode"] = "lines"
		out["tail_lines"] = tailLines
		out["stdout_tail"] = stdoutTail.Text
		out["stderr_tail"] = stderrTail.Text
		out["stdout_truncated"] = stdoutTail.LinesTruncated || stdoutTail.LineShortRead || stdoutTail.TailShortRead || stdoutTail.DroppedChunks > 0
		out["stderr_truncated"] = stderrTail.LinesTruncated || stderrTail.LineShortRead || stderrTail.TailShortRead || stderrTail.DroppedChunks > 0
		out["stdout_lines_truncated"] = stdoutTail.LinesTruncated
		out["stderr_lines_truncated"] = stderrTail.LinesTruncated
		out["stdout_line_short_read"] = stdoutTail.LineShortRead
		out["stderr_line_short_read"] = stderrTail.LineShortRead
		out["stdout_tail_short_read"] = stdoutTail.TailShortRead
		out["stderr_tail_short_read"] = stderrTail.TailShortRead
		out["stdout_dropped_chunks"] = stdoutTail.DroppedChunks
		out["stderr_dropped_chunks"] = stderrTail.DroppedChunks
		out["output_available"] = stdoutTail.Available || stderrTail.Available
	}
	return out
}

func (j *commandJob) startResult() StartNamedResult {
	var result StartNamedResult
	j.withLock(func() {
		result = StartNamedResult{
			JobID:     j.ID,
			Name:      j.Name,
			Cwd:       j.Cwd,
			Status:    j.Status,
			StartedAt: j.Started.Format(time.RFC3339),
		}
	})
	return result
}

func (j *commandJob) finish(status, errText string, result map[string]any) {
	now := time.Now().UTC()
	j.withLock(func() {
		if j.CancelRequested && (status == "failed" || status == "timed_out") {
			status = "cancelled"
			result["timed_out"] = false
		}
		if status == "failed" && errText == context.Canceled.Error() {
			status = "cancelled"
			result["timed_out"] = false
		}
		for k, v := range result {
			j.Result[k] = v
		}
		j.Finished = now
		j.Status = status
		j.Err = errText
	})
}

func (j *commandJob) requestCancel() (status string, alreadyDone bool, cancel context.CancelFunc) {
	j.withLock(func() {
		alreadyDone = !j.Finished.IsZero()
		if !alreadyDone {
			j.CancelRequested = true
			j.Status = "cancelling"
			cancel = j.cancel
		}
		status = j.Status
	})
	return status, alreadyDone, cancel
}

func (j *commandJob) cancelRequested() bool {
	var requested bool
	j.withLock(func() {
		requested = j.CancelRequested || j.Status == "cancelling"
	})
	return requested
}

func (j *commandJob) isExpired(now time.Time) bool {
	var expired bool
	j.withLock(func() {
		expired = !j.Finished.IsZero() && now.Sub(j.Finished) > finishedJobRetention
	})
	return expired
}

func (j *commandJob) withLock(fn func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fn()
}
