package shell

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
)

func jobOutputTestRunner(t *testing.T, script string, timeoutSeconds, maxOutputBytes int) *Runner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh command unavailable on Windows by default")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = timeoutSeconds
	cfg.Limits.MaxCommandOutputBytes = int64(maxOutputBytes)
	cfg.Commands = []config.CommandSpec{{Name: "job", Exec: "sh", Args: []string{"-c", script}}}
	return NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
}

func startTestJob(t *testing.T, r *Runner) string {
	t.Helper()
	started, err := r.StartNamed(json.RawMessage(`{"name":"job","cwd":"."}`))
	if err != nil {
		t.Fatalf("start named job: %v", err)
	}
	startMap, ok := started.(StartNamedResult)
	if !ok {
		t.Fatalf("expected StartNamedResult, got %T", started)
	}
	if startMap.JobID == "" {
		t.Fatal("expected job id")
	}
	return startMap.JobID
}

func readTestJob(t *testing.T, r *Runner, jobID string, tailLines int) map[string]any {
	t.Helper()
	read, err := r.JobRead(json.RawMessage(fmt.Sprintf(`{"job_id":%q,"tail_lines":%d}`, jobID, tailLines)))
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	return shellResultMap(t, read)
}

func TestJobReadReturnsLineTailForStdoutAndStderr(t *testing.T) {
	r := jobOutputTestRunner(t, "printf 'out1\\nout2\\nout3\\n'; printf 'err1\\nerr2\\nerr3\\n' >&2", 5, 10000)
	jobID := startTestJob(t, r)
	waitForJobStatus(t, r, jobID, "exited")

	result := readTestJob(t, r, jobID, 2)
	if got, _ := result["tail_mode"].(string); got != "lines" {
		t.Fatalf("expected line tail mode, got %#v", result)
	}
	if got, _ := result["tail_lines"].(int); got != 2 {
		t.Fatalf("expected tail_lines=2, got %#v", result["tail_lines"])
	}
	if got, _ := result["stdout_tail"].(string); got != "out2\nout3" {
		t.Fatalf("expected last two stdout lines, got %q", got)
	}
	if got, _ := result["stderr_tail"].(string); got != "err2\nerr3" {
		t.Fatalf("expected last two stderr lines, got %q", got)
	}
}

func TestJobReadShowsCompleteLineWhileRunning(t *testing.T) {
	r := jobOutputTestRunner(t, "printf 'first\\n'; sleep 2; printf 'second\\n'", 5, 10000)
	jobID := startTestJob(t, r)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		result := readTestJob(t, r, jobID, 10)
		if got, _ := result["stdout_tail"].(string); strings.Contains(got, "first") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected running job output to include first complete line")
}

func TestJobReadShowsCurrentPartialLineWhileRunning(t *testing.T) {
	r := jobOutputTestRunner(t, "printf partial; sleep 2; printf '\\n'", 5, 10000)
	jobID := startTestJob(t, r)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		result := readTestJob(t, r, jobID, 10)
		if got, _ := result["stdout_tail"].(string); got == "partial" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected running job output to include current partial line")
}

func TestJobReadFlushesPartialLineOnExit(t *testing.T) {
	r := jobOutputTestRunner(t, "printf no-trailing-newline", 5, 10000)
	jobID := startTestJob(t, r)
	waitForJobStatus(t, r, jobID, "exited")

	result := readTestJob(t, r, jobID, 10)
	if got, _ := result["stdout_tail"].(string); got != "no-trailing-newline" {
		t.Fatalf("expected flushed partial stdout line, got %q", got)
	}
}

func TestJobReadFlushesPartialLineOnTimeout(t *testing.T) {
	r := jobOutputTestRunner(t, "printf timeout-partial; sleep 5", 1, 10000)
	jobID := startTestJob(t, r)
	waitForJobStatus(t, r, jobID, "timed_out")

	result := readTestJob(t, r, jobID, 10)
	if got, _ := result["stdout_tail"].(string); got != "timeout-partial" {
		t.Fatalf("expected timed-out job partial output to flush, got %q", got)
	}
}

func TestJobReadFlushesPartialLineOnCancel(t *testing.T) {
	r := jobOutputTestRunner(t, "printf cancel-partial; sleep 5", 10, 10000)
	jobID := startTestJob(t, r)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result := readTestJob(t, r, jobID, 10)
		if got, _ := result["stdout_tail"].(string); got == "cancel-partial" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := r.JobCancel(json.RawMessage(fmt.Sprintf(`{"job_id":%q}`, jobID))); err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	waitForJobStatus(t, r, jobID, "cancelled")

	result := readTestJob(t, r, jobID, 10)
	if got, _ := result["stdout_tail"].(string); got != "cancel-partial" {
		t.Fatalf("expected cancelled job partial output to flush, got %q", got)
	}
}

func TestJobReadReportsLineShortRead(t *testing.T) {
	r := jobOutputTestRunner(t, "printf 'abcdefghijklmnopqrstuvwxyz\\n'", 5, 8)
	jobID := startTestJob(t, r)
	waitForJobStatus(t, r, jobID, "exited")

	result := readTestJob(t, r, jobID, 10)
	if got, _ := result["stdout_tail"].(string); got != "stuvwxyz" {
		t.Fatalf("expected suffix of overlong stdout line, got %q", got)
	}
	if got, _ := result["stdout_line_short_read"].(bool); !got {
		t.Fatalf("expected stdout_line_short_read flag, got %#v", result)
	}
	if got, _ := result["stdout_truncated"].(bool); !got {
		t.Fatalf("expected stdout_truncated flag, got %#v", result)
	}
}

func TestJobReadReportsTailShortRead(t *testing.T) {
	r := jobOutputTestRunner(t, "printf 'alpha\\nbeta\\ngamma\\n'", 5, 7)
	jobID := startTestJob(t, r)
	waitForJobStatus(t, r, jobID, "exited")

	result := readTestJob(t, r, jobID, 10)
	if got, _ := result["stdout_tail"].(string); got != "gamma" {
		t.Fatalf("expected tail byte limit to keep last line, got %q", got)
	}
	if got, _ := result["stdout_tail_short_read"].(bool); !got {
		t.Fatalf("expected stdout_tail_short_read flag, got %#v", result)
	}
}
