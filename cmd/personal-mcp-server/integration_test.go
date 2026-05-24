package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const integrationToken = "integration-token"

type integrationRuntime struct {
	root    string
	handler http.Handler
	port    int
}

func TestIntegrationMCPHTTPFilesystemWorkflow(t *testing.T) {
	rt := newIntegrationRuntime(t)
	writeTestFile(t, filepath.Join(rt.root, "notes.txt"), "alpha\nbeta match\ngamma\n")

	server := newLocalHTTPServer(t, rt.handler)

	initializeBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	assertContains(t, initializeBody, "personal-mcp-server")

	toolsBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	assertContains(t, toolsBody, "fs_read_file")
	assertContains(t, toolsBody, "tool_catalog")
	assertContains(t, toolsBody, "tool_catalog_categories")
	assertContains(t, toolsBody, "tool_catalog_category")
	assertContains(t, toolsBody, "tool_catalog_all")
	assertContains(t, toolsBody, "project_config_describe")
	assertContains(t, toolsBody, "setup_guide")
	assertContains(t, toolsBody, "guide_list")
	assertContains(t, toolsBody, "guide_read")
	assertContains(t, toolsBody, "cmd_list_named")
	assertContains(t, toolsBody, "policy_describe")
	assertContains(t, toolsBody, "project_info")
	assertContains(t, toolsBody, "workflow_list")

	resourcesBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"resource_read","arguments":{"uri":"personal-mcp://guide/project-config"}}}`)
	assertContains(t, resourcesBody, ".personal-mcp-server.toml")

	setupBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"setup_guide","arguments":{"topic":"logs"}}}`)
	assertContains(t, setupBody, "max_backups")

	guideListBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"guide_list","arguments":{}}}`)
	assertContains(t, guideListBody, "project-config-guide")

	guideReadBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":23,"method":"tools/call","params":{"name":"guide_read","arguments":{"name":"project-config"}}}`)
	assertContains(t, guideReadBody, ".personal-mcp-server.toml")

	guideSectionBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":24,"method":"tools/call","params":{"name":"guide_read","arguments":{"name":"project-config","section":"named-commands"}}}`)
	assertContains(t, guideSectionBody, "command")

	projectConfigBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":25,"method":"tools/call","params":{"name":"project_config_describe","arguments":{}}}`)
	assertContains(t, projectConfigBody, "sections")

	policyBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":26,"method":"tools/call","params":{"name":"policy_describe","arguments":{}}}`)
	assertContains(t, policyBody, "guide_read")

	catalogCategoriesBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"tool_catalog_categories","arguments":{}}}`)
	assertContains(t, catalogCategoriesBody, "filesystem_read")

	catalogCategoryBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"tool_catalog_category","arguments":{"category":"project_workflow"}}}`)
	assertContains(t, catalogCategoryBody, "cmd_run_named")

	catalogBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"tool_catalog","arguments":{}}}`)
	assertContains(t, catalogBody, "filesystem_read")
	assertContains(t, catalogBody, "cmd_run_named")

	projectInfoBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":27,"method":"tools/call","params":{"name":"project_info","arguments":{"cwd":"."}}}`)
	assertContains(t, projectInfoBody, "project")

	workflowBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":28,"method":"tools/call","params":{"name":"workflow_list","arguments":{"cwd":"."}}}`)
	assertContains(t, workflowBody, "project")

	cmdListBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":29,"method":"tools/call","params":{"name":"cmd_list_named","arguments":{"include_args":true}}}`)
	assertContains(t, cmdListBody, "global_commands")

	readBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fs_read_file","arguments":{"path":"notes.txt","max_lines":2}}}`)
	assertContains(t, readBody, "beta match")

	searchBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fs_search_text","arguments":{"query":"match","max_results":1}}}`)
	assertContains(t, searchBody, "notes.txt")

	findBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fs_find","arguments":{"include_globs":["*.txt"],"max_results":5}}}`)
	assertContains(t, findBody, "notes.txt")

	treeBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"fs_tree","arguments":{"path":".","max_depth":2}}}`)
	assertContains(t, treeBody, "notes.txt")

	escapeBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"fs_read_file","arguments":{"path":"../outside.txt"}}}`)
	if !strings.Contains(escapeBody, "IsError") && !strings.Contains(escapeBody, "outside") && !strings.Contains(escapeBody, "root") {
		t.Fatalf("expected escaped path to be rejected, got: %s", escapeBody)
	}
}

func TestIntegrationMCPHTTPBackgroundJobsWorkflow(t *testing.T) {
	rt := newIntegrationRuntime(t)
	server := newLocalHTTPServer(t, rt.handler)

	toolsBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":40,"method":"tools/list","params":{}}`)
	assertContains(t, toolsBody, "cmd_start_named")
	assertContains(t, toolsBody, "cmd_job_status")
	assertContains(t, toolsBody, "cmd_job_read")
	assertContains(t, toolsBody, "cmd_job_cancel")
	assertContains(t, toolsBody, "cmd_job_list")

	startBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":41,"method":"tools/call","params":{"name":"cmd_start_named","arguments":{"name":"job-echo","cwd":"."}}}`)
	jobID := integrationToolJobID(t, startBody)
	if jobID == "" {
		t.Fatalf("expected job_id in start response: %s", startBody)
	}

	status := waitForIntegrationJobStatus(t, server.URL, rt.port, jobID, "exited")
	if got, _ := status["exit_code"].(float64); got != 0 {
		t.Fatalf("expected job exit_code 0, got %#v from %#v", status["exit_code"], status)
	}

	readBody := postIntegrationMCP(t, server.URL, rt.port, fmt.Sprintf(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"cmd_job_read","arguments":{"job_id":%q,"tail_bytes":2000}}}`, jobID))
	read := integrationToolMap(t, readBody)
	stdout, _ := read["stdout_tail"].(string)
	if !strings.Contains(stdout, "job-output") {
		t.Fatalf("expected job output tail to contain job-output, got %#v", read)
	}

	listBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":43,"method":"tools/call","params":{"name":"cmd_job_list","arguments":{"include_finished":true}}}`)
	assertContains(t, listBody, jobID)

	startCancelBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":44,"method":"tools/call","params":{"name":"cmd_start_named","arguments":{"name":"job-sleep","cwd":"."}}}`)
	cancelJobID := integrationToolJobID(t, startCancelBody)
	if cancelJobID == "" {
		t.Fatalf("expected job_id in cancel start response: %s", startCancelBody)
	}
	cancelBody := postIntegrationMCP(t, server.URL, rt.port, fmt.Sprintf(`{"jsonrpc":"2.0","id":45,"method":"tools/call","params":{"name":"cmd_job_cancel","arguments":{"job_id":%q}}}`, cancelJobID))
	assertContains(t, cancelBody, "cancelling")
	waitForIntegrationJobStatus(t, server.URL, rt.port, cancelJobID, "cancelled")
}

func TestIntegrationMCPHTTPPersistentShellPoolWorkflow(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash unavailable")
	}
	rt := newIntegrationRuntime(t)
	writeTestFile(t, filepath.Join(rt.root, ".personal-mcp-server.toml"), `config_kind = "project"
config_version = 1

[project]
name = "integration-pool"

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
`)

	server := newLocalHTTPServer(t, rt.handler)

	listBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":50,"method":"tools/call","params":{"name":"cmd_list_named","arguments":{"cwd":".","include_args":true}}}`)
	assertContains(t, listBody, "pool-slow")
	assertContains(t, listBody, "persistent_shell")

	startSlowBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":51,"method":"tools/call","params":{"name":"cmd_start_named","arguments":{"name":"pool-slow","cwd":"."}}}`)
	slowJobID := integrationToolJobID(t, startSlowBody)
	if slowJobID == "" {
		t.Fatalf("expected slow job_id: %s", startSlowBody)
	}
	waitForFile(t, filepath.Join(rt.root, "pool-started.txt"))

	startFastBody := postIntegrationMCP(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":52,"method":"tools/call","params":{"name":"cmd_start_named","arguments":{"name":"pool-fast","cwd":"."}}}`)
	fastJobID := integrationToolJobID(t, startFastBody)
	if fastJobID == "" {
		t.Fatalf("expected fast job_id: %s", startFastBody)
	}

	fastStatus := waitForIntegrationJobStatus(t, server.URL, rt.port, fastJobID, "exited")
	if fastStatus["timeout_phase"] == "persistent_shell_busy" {
		t.Fatalf("fast pooled job should use a second shell instead of timing out busy: %#v", fastStatus)
	}
	fastReadBody := postIntegrationMCP(t, server.URL, rt.port, fmt.Sprintf(`{"jsonrpc":"2.0","id":53,"method":"tools/call","params":{"name":"cmd_job_read","arguments":{"job_id":%q,"tail_bytes":2000}}}`, fastJobID))
	fastRead := integrationToolMap(t, fastReadBody)
	if stdout, _ := fastRead["stdout_tail"].(string); !strings.Contains(stdout, "pool-fast") {
		t.Fatalf("expected fast pooled output, got %#v", fastRead)
	}

	waitForIntegrationJobStatus(t, server.URL, rt.port, slowJobID, "exited")
}

func TestIntegrationSecurityRejectsMissingAuthBadHostAndOrigin(t *testing.T) {
	rt := newIntegrationRuntime(t)
	server := newLocalHTTPServer(t, rt.handler)

	req := newIntegrationRequest(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	resp := doIntegrationRequest(t, req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected missing auth status 401, got %d", resp.StatusCode)
	}
	closeResponseBody(t, resp)

	req = newIntegrationRequest(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	req.Host = "evil.test"
	req.Header.Set("Authorization", "Bearer "+integrationToken)
	resp = doIntegrationRequest(t, req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected bad host status 403, got %d", resp.StatusCode)
	}
	closeResponseBody(t, resp)

	req = newIntegrationRequest(t, server.URL, rt.port, `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
	req.Header.Set("Authorization", "Bearer "+integrationToken)
	req.Header.Set("Origin", "http://evil.test")
	resp = doIntegrationRequest(t, req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected bad origin status 403, got %d", resp.StatusCode)
	}
	closeResponseBody(t, resp)
}

func TestSmokeVersionSubprocess(t *testing.T) {
	out := runMainHelper(t, "version")
	assertContains(t, out, version)
}

func TestSmokeServeSubprocessHealthAndToolCall(t *testing.T) {
	port := freeLocalPort(t)
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "smoke.txt"), "smoke test\n")
	configPath := writeIntegrationConfig(t, root, port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestMainHelperProcess", "--", "serve", "--config", configPath, "--reload-interval", "0") //nolint:gosec // Test helper re-execs the current test binary with fixed arguments.
	cmd.Env = append(os.Environ(), "GO_WANT_PERSONAL_MCP_HELPER=1", "PERSONAL_MCP_TOKEN="+integrationToken)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper server: %v", err)
	}
	defer stopProcess(t, cmd)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHealthz(t, baseURL+"/healthz")
	body := postIntegrationMCP(t, baseURL, port, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"server_info","arguments":{}}}`)
	assertContains(t, body, "personal-mcp-server")

	startBody := postIntegrationMCP(t, baseURL, port, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"cmd_start_named","arguments":{"name":"job-echo","cwd":"."}}}`)
	jobID := integrationToolJobID(t, startBody)
	waitForIntegrationJobStatus(t, baseURL, port, jobID, "exited")
	readBody := postIntegrationMCP(t, baseURL, port, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"cmd_job_read","arguments":{"job_id":%q,"tail_bytes":2000}}}`, jobID))
	assertContains(t, readBody, "job-output")
}

func TestMainHelperProcess(_ *testing.T) {
	if os.Getenv("GO_WANT_PERSONAL_MCP_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"personal-mcp-server"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	os.Exit(2)
}

func newIntegrationRuntime(t *testing.T) integrationRuntime {
	t.Helper()
	port := 3929
	root := t.TempDir()
	configPath := writeIntegrationConfig(t, root, port)
	t.Setenv("PERSONAL_MCP_TOKEN", integrationToken)
	rt, err := buildRuntime(configPath, "")
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(rt.Close)
	handler := rt.handler
	return integrationRuntime{root: root, handler: handler, port: port}
}

func writeIntegrationConfig(t *testing.T, root string, port int) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	trustStore := filepath.Join(t.TempDir(), "trusted-projects.toml")
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
command_timeout_seconds = 5
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
args = ["-c", "sleep 5; printf late"]
`, root, port, auditPath, trustStore, root)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("file %s was not created before deadline", path)
}

func waitForIntegrationJobStatus(t *testing.T, baseURL string, port int, jobID, wantStatus string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		body := postIntegrationMCP(t, baseURL, port, fmt.Sprintf(`{"jsonrpc":"2.0","id":90,"method":"tools/call","params":{"name":"cmd_job_status","arguments":{"job_id":%q}}}`, jobID))
		last = integrationToolMap(t, body)
		if status, _ := last["status"].(string); status == wantStatus {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach status %q, last status %#v", jobID, wantStatus, last)
	return nil
}

func integrationToolJobID(t *testing.T, body string) string {
	t.Helper()
	value, _ := integrationToolMap(t, body)["job_id"].(string)
	return value
}

func integrationToolMap(t *testing.T, body string) map[string]any {
	t.Helper()
	text := integrationToolText(t, body)
	out := map[string]any{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode tool JSON text: %v\nresponse: %s\ntext: %s", err, body, text)
	}
	return out
}

func integrationToolText(t *testing.T, body string) string {
	t.Helper()
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
		t.Fatalf("decode MCP response: %v\n%s", err, body)
	}
	if resp.Error != nil {
		t.Fatalf("MCP response error: %#v\n%s", resp.Error, body)
	}
	if resp.Result.IsError {
		t.Fatalf("tool returned error: %s", body)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("tool response contained no text content: %s", body)
	}
	return resp.Result.Content[0].Text
}

func postIntegrationMCP(t *testing.T, baseURL string, port int, body string) string {
	t.Helper()
	req := newIntegrationRequest(t, baseURL, port, body)
	req.Header.Set("Authorization", "Bearer "+integrationToken)
	resp := doIntegrationRequest(t, req)
	defer closeResponseBody(t, resp)
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(b))
	}
	return string(b)
}

func newIntegrationRequest(t *testing.T, baseURL string, port int, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL+"/mcp", strings.NewReader(body)) // #nosec G107 G704 -- integration tests only contact httptest/local personal-mcp-server servers.
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = fmt.Sprintf("127.0.0.1:%d", port)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func doIntegrationRequest(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req) // #nosec G107 G704 -- request is built by local integration test helpers.
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func closeResponseBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir test file parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local TCP bind unavailable in this environment: %v", err)
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Fatalf("close listener: %v", closeErr)
		}
	}()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", listener.Addr())
	}
	return addr.Port
}

func runMainHelper(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmdArgs := append([]string{"-test.run=TestMainHelperProcess", "--"}, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...) //nolint:gosec // Test helper re-execs the current test binary with controlled test arguments.
	cmd.Env = append(os.Environ(), "GO_WANT_PERSONAL_MCP_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run helper %v: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func waitForHealthz(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody) // #nosec G107 G704 -- smoke tests poll a local health endpoint.
		if err != nil {
			t.Fatalf("new health request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req) // #nosec G107 G704 -- request is built by local integration test helpers.
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				closeResponseBody(t, resp)
				return
			}
			closeResponseBody(t, resp)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not become healthy at %s", url)
}

func stopProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil {
		return
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	if err := cmd.Process.Signal(os.Interrupt); err != nil && !processAlreadyFinished(err) {
		t.Logf("interrupt helper process: %v", err)
	}

	select {
	case err := <-waitDone:
		logProcessExit(t, "helper process stopped", err)
		return
	case <-time.After(2 * time.Second):
	}

	if err := cmd.Process.Kill(); err != nil && !processAlreadyFinished(err) {
		t.Fatalf("kill helper process: %v", err)
	}
	logProcessExit(t, "helper process killed", <-waitDone)
}

func processAlreadyFinished(err error) bool {
	return err != nil && strings.Contains(err.Error(), "process already finished")
}

func logProcessExit(t *testing.T, msg string, err error) {
	t.Helper()
	if err != nil {
		t.Logf("%s: %v", msg, err)
	}
}
