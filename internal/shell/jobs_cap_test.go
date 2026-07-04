package shell

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
)

func jobCapTestRunner(t *testing.T, script string) *Runner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh command unavailable on Windows by default")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 10
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.Commands = []config.CommandSpec{{Name: "job", Exec: "sh", Args: []string{"-c", script}}}
	return NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
}

func startCapTestJob(t *testing.T, r *Runner) any {
	t.Helper()
	out, err := r.StartNamed(json.RawMessage(`{"name":"job","cwd":"."}`))
	if err != nil {
		t.Fatalf("start named job: %v", err)
	}
	return out
}

func TestStartNamedReturnsBusyAtBackgroundJobCap(t *testing.T) {
	r := jobCapTestRunner(t, "sleep 5")
	jobIDs := make([]string, 0, defaultMaxBackgroundJobs)
	for i := 0; i < defaultMaxBackgroundJobs; i++ {
		started := startCapTestJob(t, r)
		startMap, ok := started.(StartNamedResult)
		if !ok {
			t.Fatalf("start %d: expected StartNamedResult, got %T %#v", i, started, started)
		}
		jobIDs = append(jobIDs, startMap.JobID)
	}

	busy := shellResultMap(t, startCapTestJob(t, r))
	if got, _ := busy["busy"].(bool); !got {
		t.Fatalf("expected busy result, got %#v", busy)
	}
	if got, _ := busy["retryable"].(bool); !got {
		t.Fatalf("expected retryable busy result, got %#v", busy)
	}
	if got, _ := busy["error"].(string); got != "too_many_background_jobs" {
		t.Fatalf("expected too_many_background_jobs, got %#v", busy)
	}
	if got, _ := busy["active_jobs"].(int); got != defaultMaxBackgroundJobs {
		t.Fatalf("expected active_jobs=%d, got %#v", defaultMaxBackgroundJobs, busy["active_jobs"])
	}
	if got, _ := busy["max_background_jobs"].(int); got != defaultMaxBackgroundJobs {
		t.Fatalf("expected max_background_jobs=%d, got %#v", defaultMaxBackgroundJobs, busy["max_background_jobs"])
	}

	for _, jobID := range jobIDs {
		if _, err := r.JobCancel(json.RawMessage(fmt.Sprintf(`{"job_id":%q}`, jobID))); err != nil {
			t.Fatalf("cancel job %s: %v", jobID, err)
		}
	}
}

func TestFinishedBackgroundJobsDoNotCountAgainstCap(t *testing.T) {
	r := jobCapTestRunner(t, "printf done")
	for i := 0; i < defaultMaxBackgroundJobs; i++ {
		started := startCapTestJob(t, r)
		startMap, ok := started.(StartNamedResult)
		if !ok {
			t.Fatalf("start %d: expected StartNamedResult, got %T %#v", i, started, started)
		}
		waitForJobStatus(t, r, startMap.JobID, "exited")
	}

	started := startCapTestJob(t, r)
	if _, ok := started.(StartNamedResult); !ok {
		t.Fatalf("finished jobs should not count against cap; got %T %#v", started, started)
	}
}

func TestConcurrentBackgroundStartsCannotExceedCap(t *testing.T) {
	r := jobCapTestRunner(t, "sleep 5")
	const attempts = defaultMaxBackgroundJobs + 8
	var wg sync.WaitGroup
	results := make(chan any, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := r.StartNamed(json.RawMessage(`{"name":"job","cwd":"."}`))
			if err != nil {
				results <- err
				return
			}
			results <- out
		}()
	}
	wg.Wait()
	close(results)

	started := 0
	busy := 0
	var jobIDs []string
	for result := range results {
		switch v := result.(type) {
		case StartNamedResult:
			started++
			jobIDs = append(jobIDs, v.JobID)
		case map[string]any:
			if isBusy, _ := v["busy"].(bool); isBusy {
				busy++
			} else {
				t.Fatalf("unexpected map result: %#v", v)
			}
		case error:
			t.Fatalf("unexpected start error: %v", v)
		default:
			t.Fatalf("unexpected result type %T %#v", result, result)
		}
	}
	if started != defaultMaxBackgroundJobs {
		t.Fatalf("expected exactly %d started jobs, got %d", defaultMaxBackgroundJobs, started)
	}
	if busy != attempts-defaultMaxBackgroundJobs {
		t.Fatalf("expected %d busy results, got %d", attempts-defaultMaxBackgroundJobs, busy)
	}
	for _, jobID := range jobIDs {
		_, _ = r.JobCancel(json.RawMessage(fmt.Sprintf(`{"job_id":%q}`, jobID)))
	}
}

func TestPruneFinishedJobsDoesNotHoldRegistryLockWhileInspectingJobs(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	oldJob := &commandJob{ID: "old", Name: "old", Started: time.Now().Add(-2 * finishedJobRetention), Finished: time.Now().Add(-finishedJobRetention - time.Second), Status: "exited"}
	freshJob := &commandJob{ID: "fresh", Name: "fresh", Started: time.Now(), Finished: time.Now(), Status: "exited"}
	r.jobs = map[string]*commandJob{"old": oldJob, "fresh": freshJob}

	r.pruneFinishedJobs(time.Now())

	r.jobMu.Lock()
	_, oldPresent := r.jobs["old"]
	_, freshPresent := r.jobs["fresh"]
	r.jobMu.Unlock()
	if oldPresent {
		t.Fatal("expected expired job to be pruned")
	}
	if !freshPresent {
		t.Fatal("expected fresh finished job to remain")
	}
}
