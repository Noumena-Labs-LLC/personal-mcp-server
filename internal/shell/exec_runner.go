package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

type RunArgvArgs struct {
	Exec string   `json:"exec"`
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

func (r *Runner) RunArgv(raw json.RawMessage) (any, error) {
	return r.runArgv(context.Background(), raw)
}

func (r *Runner) runArgv(ctx context.Context, raw json.RawMessage) (any, error) {
	var a RunArgvArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Exec == "" {
		return nil, errors.New("exec is required")
	}
	if containsShellSyntax(a.Exec) {
		return nil, errors.New("exec must be an executable name or path, not shell syntax")
	}
	for _, arg := range a.Args {
		if strings.Contains(arg, "\x00") {
			return nil, errors.New("args cannot contain NUL")
		}
	}
	cwd, err := r.resolveCwd(a.Cwd)
	if err != nil {
		return nil, err
	}
	decision, err := policy.DecideCommand(r.Cfg.CommandPolicy, a.Exec, a.Args)
	if err != nil {
		return nil, err
	}
	switch decision.Action {
	case policy.ActionAllow:
	case policy.ActionDeny:
		return nil, fmt.Errorf("command policy denied %s %s: %s", a.Exec, policy.JoinArgs(a.Args), decision.Rule)
	case policy.ActionPrompt:
		if !r.Cfg.Approval.Enabled || r.Approver == nil {
			return nil, fmt.Errorf("command policy requires approval for %s %s, but approval is disabled", a.Exec, policy.JoinArgs(a.Args))
		}
		_, err := r.Approver.Request(ctx, approval.Request{
			Kind:    "command",
			Action:  "run",
			Rule:    decision.Rule,
			Summary: a.Exec + " " + policy.JoinArgs(a.Args),
			Details: map[string]any{"exec": a.Exec, "args": a.Args, "cwd": a.Cwd, "resolved_cwd": cwd},
		})
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("command policy returned unknown action %q", decision.Action)
	}
	return r.runExec(ctx, a.Exec, a.Args, cwd, map[string]string{}, nil, map[string]any{"exec": a.Exec, "args": a.Args, "cwd": a.Cwd, "policy_action": decision.Action, "policy_rule": decision.Rule})
}

func containsShellSyntax(s string) bool {
	return strings.ContainsAny(s, ";&|`$><\n")
}

func (r *Runner) runExec(parentCtx context.Context, execName string, args []string, cwd string, env map[string]string, envFromHost []string, extra map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(r.Cfg.Limits.CommandTimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, execName, args...)
	cmd.Dir = cwd
	cmd.Env = buildEnvFromParts(env, envFromHost)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	var stdout, stderr limitBuffer
	stdout.Limit = r.Cfg.Limits.MaxCommandOutputBytes
	stderr.Limit = r.Cfg.Limits.MaxCommandOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
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
	exitCode := 0
	if waitErr != nil {
		exitCode = -1
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		}
	}
	result := map[string]any{
		"exit_code": exitCode, "timed_out": timedOut, "duration_ms": time.Since(started).Milliseconds(), "configured_timeout_seconds": r.Cfg.Limits.CommandTimeoutSeconds,
		"stdout": stdout.String(), "stderr": stderr.String(), "stdout_truncated": stdout.Truncated, "stderr_truncated": stderr.Truncated,
	}
	if timedOut {
		result["timeout_phase"] = "command_run"
		result["timeout_guidance"] = commandTimeoutGuidance()
	}
	for k, v := range extra {
		result[k] = v
	}
	return result, nil
}

func commandTimeoutGuidance() string {
	return "Command reached the configured timeout. Prefer a narrower named command or increase [limits].command_timeout_seconds in trusted local config after reviewing slow-tool diagnostics."
}

func buildEnvFromParts(fixed map[string]string, fromHost []string) []string {
	env := []string{"PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", "HOME="}
	for k, v := range fixed {
		env = append(env, k+"="+v)
	}
	for _, name := range fromHost {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

type limitBuffer struct {
	buf       bytes.Buffer
	Limit     int64
	Truncated bool
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	remaining := b.Limit - int64(b.buf.Len())
	if remaining <= 0 {
		b.Truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		b.Truncated = true
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitBuffer) String() string { return b.buf.String() }
