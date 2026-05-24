package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStressServeStartupShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stress tests are not portable to Windows in this repository")
	}

	for round := 0; round < 3; round++ {
		port := freeLocalPort(t)
		root := t.TempDir()
		configPath := writeStressConfig(t, root, port)
		cmd := startStressHelper(t, configPath)
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

		waitForHealthz(t, baseURL+"/healthz")
		body := postIntegrationMCP(t, baseURL, port, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		assertContains(t, body, "server_info")
		assertContains(t, body, "cmd_start_named")

		stopProcess(t, cmd)
	}
}

func TestStressConcurrentMCPRequests(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stress tests are not portable to Windows in this repository")
	}

	port := freeLocalPort(t)
	root := t.TempDir()
	configPath := writeStressConfig(t, root, port)
	cmd := startStressHelper(t, configPath)
	defer stopProcess(t, cmd)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHealthz(t, baseURL+"/healthz")

	const workers = 6
	const iterations = 12
	errs := make(chan error, workers*iterations)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iter := 0; iter < iterations; iter++ {
				var body string
				var want string
				var err error
				switch (worker + iter) % 4 {
				case 0:
					body, err = stressPostMCP(baseURL, port, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
					want = "cmd_start_named"
				case 1:
					body, err = stressPostMCP(baseURL, port, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"server_info","arguments":{}}}`)
					want = "personal-mcp-server"
				case 2:
					body, err = stressPostMCP(baseURL, port, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"guide_list","arguments":{}}}`)
					want = "project-config-guide"
				default:
					body, err = stressPostMCP(baseURL, port, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"resource_read","arguments":{"uri":"personal-mcp://guide/project-config"}}}`)
					want = ".personal-mcp-server.toml"
				}
				if err != nil {
					errs <- fmt.Errorf("worker %d iteration %d: %w", worker, iter, err)
					return
				}
				if !strings.Contains(body, want) {
					errs <- fmt.Errorf("worker %d iteration %d: unexpected body: %s", worker, iter, body)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestStressJobChurn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stress tests are not portable to Windows in this repository")
	}

	port := freeLocalPort(t)
	root := t.TempDir()
	configPath := writeStressConfig(t, root, port)
	cmd := startStressHelper(t, configPath)
	defer stopProcess(t, cmd)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHealthz(t, baseURL+"/healthz")

	const workers = 4
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			name := "job-echo"
			if worker%2 == 1 {
				name = "job-sleep"
			}
			startBody, err := stressPostMCP(baseURL, port, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"cmd_start_named","arguments":{"name":%q,"cwd":"."}}}`, 10+worker, name))
			if err != nil {
				errs <- fmt.Errorf("worker %d start: %w", worker, err)
				return
			}
			jobID, err := stressJobID(startBody)
			if err != nil {
				errs <- fmt.Errorf("worker %d job id: %w", worker, err)
				return
			}

			if name == "job-sleep" {
				cancelBody, err := stressPostMCP(baseURL, port, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"cmd_job_cancel","arguments":{"job_id":%q}}}`, 20+worker, jobID))
				if err != nil {
					errs <- fmt.Errorf("worker %d cancel: %w", worker, err)
					return
				}
				if !strings.Contains(cancelBody, "cancelling") && !strings.Contains(cancelBody, "cancelled") {
					errs <- fmt.Errorf("worker %d cancel body: %s", worker, cancelBody)
					return
				}
				if _, err := stressWaitForJobStatus(baseURL, port, jobID, "cancelled", 5*time.Second); err != nil {
					errs <- fmt.Errorf("worker %d wait cancelled: %w", worker, err)
					return
				}
				return
			}

			status, err := stressWaitForJobStatus(baseURL, port, jobID, "exited", 5*time.Second)
			if err != nil {
				errs <- fmt.Errorf("worker %d wait exited: %w", worker, err)
				return
			}
			if got, _ := status["exit_code"].(float64); got != 0 {
				errs <- fmt.Errorf("worker %d exit code = %#v", worker, status["exit_code"])
				return
			}
			readBody, err := stressPostMCP(baseURL, port, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"cmd_job_read","arguments":{"job_id":%q,"tail_bytes":2000}}}`, 30+worker, jobID))
			if err != nil {
				errs <- fmt.Errorf("worker %d read: %w", worker, err)
				return
			}
			if !strings.Contains(readBody, "job-output") {
				errs <- fmt.Errorf("worker %d read body: %s", worker, readBody)
				return
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestStressPersistentShellPoolContention(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stress tests are not portable to Windows in this repository")
	}

	port := freeLocalPort(t)
	root := t.TempDir()
	configPath := writeStressConfig(t, root, port)
	writeStressProjectConfig(t, root)
	cmd := startStressHelper(t, configPath)
	defer stopProcess(t, cmd)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHealthz(t, baseURL+"/healthz")

	const workers = 4
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			name := "pool-slow"
			if worker%2 == 1 {
				name = "pool-fast"
			}
			startBody, err := stressPostMCP(baseURL, port, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"cmd_start_named","arguments":{"name":%q,"cwd":"."}}}`, 40+worker, name))
			if err != nil {
				errs <- fmt.Errorf("worker %d start: %w", worker, err)
				return
			}
			payload, err := stressToolMap(startBody)
			if err != nil {
				errs <- fmt.Errorf("worker %d parse start: %w", worker, err)
				return
			}
			if busy, _ := payload["busy"].(bool); busy {
				if retryable, _ := payload["retryable"].(bool); !retryable {
					errs <- fmt.Errorf("worker %d busy response was not retryable: %#v", worker, payload)
				}
				return
			}
			jobID, _ := payload["job_id"].(string)
			if jobID == "" {
				errs <- fmt.Errorf("worker %d missing job_id: %#v", worker, payload)
				return
			}
			status, err := stressWaitForJobStatus(baseURL, port, jobID, "exited", 5*time.Second)
			if err != nil {
				errs <- fmt.Errorf("worker %d wait exited: %w", worker, err)
				return
			}
			if got, _ := status["exit_code"].(float64); got != 0 {
				errs <- fmt.Errorf("worker %d exit code = %#v", worker, status["exit_code"])
				return
			}
			readBody, err := stressPostMCP(baseURL, port, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"cmd_job_read","arguments":{"job_id":%q,"tail_bytes":2000}}}`, 50+worker, jobID))
			if err != nil {
				errs <- fmt.Errorf("worker %d read: %w", worker, err)
				return
			}
			if !strings.Contains(readBody, name) {
				errs <- fmt.Errorf("worker %d read body: %s", worker, readBody)
				return
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func startStressHelper(t *testing.T, configPath string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestMainHelperProcess", "--", "serve", "--config", configPath, "--reload-interval", "0") //nolint:gosec // stress tests re-exec the current test binary with fixed arguments.
	cmd.Env = append(os.Environ(), "GO_WANT_PERSONAL_MCP_HELPER=1", "PERSONAL_MCP_TOKEN="+integrationToken)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper server: %v", err)
	}
	return cmd
}

func writeStressConfig(t *testing.T, root string, port int) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "stress-config.toml")
	auditPath := filepath.Join(t.TempDir(), "stress-audit.log")
	trustStore := filepath.Join(t.TempDir(), "stress-trusted-projects.toml")
	content := fmt.Sprintf(`config_version = 1
roots = [%q]

[server]
host = "127.0.0.1"
port = %d
endpoint = "/mcp"
auth_token_env = "PERSONAL_MCP_TOKEN"
validate_origin = true
allowed_origins = ["http://127.0.0.1", "http://localhost"]

[audit]
path = %q
max_bytes = 1048576
max_backups = 2

[approval]
enabled = false
timeout_seconds = 1
default_on_timeout = "deny"
remember_session_decisions = false

[project_configs]
enabled = true
filename = ".personal-mcp-server.toml"
auto_load = false
trust_store = %q
trusted_projects = [%q]

[limits]
max_read_bytes = 200000
max_write_bytes = 50000
max_search_results = 100
max_search_file_bytes = 1000000
command_timeout_seconds = 2
max_command_output_bytes = 200000
diff_context_lines = 3
max_diff_bytes = 200000
max_patch_bytes = 200000

[tools.server_info]
enabled = true
[tools.policy_describe]
enabled = true
[tools.fs_list_roots]
enabled = true
[tools.fs_read_file]
enabled = true
[tools.fs_search_text]
enabled = true
[tools.fs_find]
enabled = true
[tools.fs_tree]
enabled = true
[tools.file_explain_policy]
enabled = true
[tools.resource_list]
enabled = true
[tools.resource_read]
enabled = true
[tools.project_config_describe]
enabled = true
[tools.setup_guide]
enabled = true
[tools.project_info]
enabled = true
[tools.workflow_list]
enabled = true
[tools.cmd_list_named]
enabled = true
[tools.cmd_run_named]
enabled = true

[file_policy]
read_default = "allow"
write_default = "deny"
create_default = "deny"
patch_default = "deny"
unified_patch_default = "deny"

[command_policy]
default = "deny"

[command_environment]
allow_persistent_shell = true
allowed_shells = ["/bin/bash", "/bin/sh"]
persistent_shell_pool_size = 2
persistent_shell_acquire_timeout_seconds = 1

[[commands]]
name = "job-echo"
exec = "/bin/sh"
args = ["-c", "printf job-output"]

[[commands]]
name = "job-sleep"
exec = "/bin/sh"
args = ["-c", "sleep 1; printf late"]
`, root, port, auditPath, trustStore, root)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write stress config: %v", err)
	}
	return configPath
}

func writeStressProjectConfig(t *testing.T, root string) {
	t.Helper()
	content := `config_kind = "project"
config_version = 1

[project]
name = "stress-project"

[command_environment]
run_mode = "persistent_shell"
shell = "/bin/bash"

[[commands]]
name = "pool-slow"
exec = "/bin/sh"
args = ["-c", "printf started > pool-started.txt; sleep 1; printf pool-slow"]

[[commands]]
name = "pool-fast"
exec = "/bin/sh"
args = ["-c", "printf pool-fast"]
`
	if err := os.WriteFile(filepath.Join(root, ".personal-mcp-server.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write stress project config: %v", err)
	}
}

func stressPostMCP(baseURL string, port int, body string) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL+"/mcp", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Host = fmt.Sprintf("127.0.0.1:%d", port)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+integrationToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("expected status 200, got %d: %s", resp.StatusCode, string(b))
	}
	return string(b), nil
}

func stressToolText(body string) (string, error) {
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return "", fmt.Errorf("decode MCP response: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("MCP response error: %#v", resp.Error)
	}
	if resp.Result.IsError {
		return "", fmt.Errorf("tool returned error: %s", body)
	}
	if len(resp.Result.Content) == 0 {
		return "", fmt.Errorf("tool response contained no text content: %s", body)
	}
	return resp.Result.Content[0].Text, nil
}

func stressToolMap(body string) (map[string]any, error) {
	text, err := stressToolText(body)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("decode tool JSON text: %w", err)
	}
	return out, nil
}

func stressJobID(body string) (string, error) {
	payload, err := stressToolMap(body)
	if err != nil {
		return "", err
	}
	jobID, _ := payload["job_id"].(string)
	if jobID == "" {
		return "", fmt.Errorf("expected job_id in response: %s", body)
	}
	return jobID, nil
}

func stressWaitForJobStatus(baseURL string, port int, jobID, wantStatus string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	var last map[string]any
	for time.Now().Before(deadline) {
		body, err := stressPostMCP(baseURL, port, fmt.Sprintf(`{"jsonrpc":"2.0","id":90,"method":"tools/call","params":{"name":"cmd_job_status","arguments":{"job_id":%q}}}`, jobID))
		if err != nil {
			return nil, err
		}
		last, err = stressToolMap(body)
		if err != nil {
			return nil, err
		}
		if status, _ := last["status"].(string); status == wantStatus {
			return last, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("job %s did not reach status %q, last status %#v", jobID, wantStatus, last)
}
