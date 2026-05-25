package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
)

type Runner struct {
	Cfg      *config.Config
	Sandbox  *fsx.Sandbox
	Specs    map[string]config.CommandSpec
	Approver *approval.Manager
	Projects *project.Manager

	shellMu    sync.Mutex
	shellPools map[string]*persistentShellPool

	jobMu sync.Mutex
	jobs  map[string]*commandJob
}

type persistentShellPool struct {
	key      string
	mu       sync.Mutex
	sessions []*persistentShell
	starting int
}

var errPersistentShellBusy = errors.New("persistent shell busy")

const extraArgsPlaceholder = "{{extra_args}}"

func NewRunner(c *config.Config, s *fsx.Sandbox, approver *approval.Manager, projects *project.Manager) *Runner {
	specs := map[string]config.CommandSpec{}
	for i := range c.Commands {
		cmd := &c.Commands[i]
		specs[cmd.Name] = *cmd
	}
	return &Runner{Cfg: c, Sandbox: s, Specs: specs, Approver: approver, Projects: projects, shellPools: map[string]*persistentShellPool{}, jobs: map[string]*commandJob{}}
}

func (r *Runner) resolveCwd(cwd string) (string, error) {
	resolved, err := r.Sandbox.ResolveWithCwd(".", cwd)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", cwd)
	}
	return resolved, nil
}

type RunArgs struct {
	Name      string   `json:"name"`
	Cwd       string   `json:"cwd"`
	ExtraArgs []string `json:"extra_args"`
}

func (r *Runner) RunNamed(raw json.RawMessage) (any, error) {
	return r.runNamed(context.Background(), raw)
}

func (r *Runner) runNamed(ctx context.Context, raw json.RawMessage) (any, error) {
	prepared, err := r.prepareNamedCommand(raw)
	if err != nil {
		return nil, err
	}
	runMode := commandRunMode(prepared.Spec)
	prepared.Extra["run_mode"] = runMode
	if runMode == "persistent_shell" {
		return r.runPersistentShell(ctx, prepared.Spec, prepared.FinalArgs, prepared.Cwd, prepared.Source, prepared.ProjectState, prepared.Extra)
	}
	return r.runExec(ctx, prepared.Spec.Exec, prepared.FinalArgs, prepared.Cwd, prepared.Spec.Env, prepared.Spec.EnvFromHost, prepared.Extra)
}

type SequenceArgs struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
}

func (r *Runner) RunSequence(raw json.RawMessage) (any, error) {
	var a SequenceArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Name) == "" {
		return nil, errors.New("name is required")
	}
	seq, source, state, ok := r.lookupSequence(a.Name, a.Cwd)
	if !ok {
		return nil, fmt.Errorf("unknown command sequence %q", a.Name)
	}
	mode := seq.Mode
	if mode == "" {
		mode = "stop_on_failure"
	}
	if mode != "stop_on_failure" && mode != "continue" {
		return nil, fmt.Errorf("command sequence %q has invalid mode %q", a.Name, mode)
	}
	steps := []map[string]any{}
	failed := false
	for i, step := range seq.Steps {
		rawStep, err := json.Marshal(RunArgs{Name: step.Name, Cwd: a.Cwd, ExtraArgs: step.ExtraArgs})
		if err != nil {
			return nil, err
		}
		out, err := r.runNamed(context.Background(), rawStep)
		entry := map[string]any{"index": i, "name": step.Name}
		if err != nil {
			entry["error"] = err.Error()
			failed = true
			steps = append(steps, entry)
			if mode == "stop_on_failure" {
				break
			}
			continue
		}
		entry["result"] = out
		if m, ok := out.(map[string]any); ok {
			if code, ok := m["exit_code"].(int); ok && code != 0 {
				failed = true
				entry["failed"] = true
				steps = append(steps, entry)
				if mode == "stop_on_failure" {
					break
				}
				continue
			}
		}
		steps = append(steps, entry)
	}
	res := map[string]any{"name": a.Name, "cwd": a.Cwd, "mode": mode, "command_source": source, "ok": !failed, "steps": steps}
	if state.Found {
		res["project"] = map[string]any{"root": state.Root, "trusted": state.Trusted}
	}
	return res, nil
}

type preparedNamedCommand struct {
	Args         RunArgs
	Spec         config.CommandSpec
	Source       string
	ProjectState project.State
	Cwd          string
	FinalArgs    []string
	Extra        map[string]any
}

func (r *Runner) prepareNamedCommand(raw json.RawMessage) (preparedNamedCommand, error) {
	var a RunArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return preparedNamedCommand{}, err
	}
	if strings.TrimSpace(a.Name) == "" {
		return preparedNamedCommand{}, errors.New("name is required")
	}
	spec, source, projectState, ok := r.lookupNamed(a.Name, a.Cwd)
	if !ok {
		return preparedNamedCommand{}, fmt.Errorf("unknown command %q. Use cmd_list_named to discover available commands. Available commands: %s", a.Name, strings.Join(r.availableCommandNames(a.Cwd), ", "))
	}
	spec = effectiveCommandSpec(spec, source, projectState)
	effectiveCwdInput, cwdSource, err := r.effectiveCommandCwd(a.Cwd, spec, source, projectState)
	if err != nil {
		return preparedNamedCommand{}, err
	}
	cwd, err := r.resolveCwd(effectiveCwdInput)
	if err != nil {
		return preparedNamedCommand{}, err
	}
	finalArgs, err := r.finalCommandArgs(spec, a.ExtraArgs, effectiveCwdInput, projectState)
	if err != nil {
		return preparedNamedCommand{}, err
	}
	extra := map[string]any{"name": a.Name, "command_source": source, "argv": append([]string{spec.Exec}, finalArgs...), "cwd": effectiveCwdInput, "resolved_cwd": cwd, "cwd_source": cwdSource}
	if projectState.Found {
		extra["project"] = map[string]any{"root": projectState.Root, "trusted": projectState.Trusted}
	}
	return preparedNamedCommand{
		Args:         a,
		Spec:         spec,
		Source:       source,
		ProjectState: projectState,
		Cwd:          cwd,
		FinalArgs:    finalArgs,
		Extra:        extra,
	}, nil
}

func (r *Runner) effectiveCommandCwd(callCwd string, spec config.CommandSpec, source string, state project.State) (cwd, cwdSource string, err error) {
	if strings.TrimSpace(callCwd) != "" {
		return callCwd, "tool_call", nil
	}
	configured := strings.TrimSpace(spec.Cwd)
	if configured == "" {
		return "", "", errors.New("cwd is required unless the named command config sets cwd")
	}
	if strings.Contains(configured, "\x00") {
		return "", "", fmt.Errorf("command %q cwd contains NUL", spec.Name)
	}
	if source == "project" {
		if !state.Found || strings.TrimSpace(state.Root) == "" {
			return "", "", fmt.Errorf("command %q configured cwd requires a discovered project root", spec.Name)
		}
		if filepath.IsAbs(configured) {
			return "", "", fmt.Errorf("project command %q cwd must be relative", spec.Name)
		}
		return filepath.Join(state.Root, configured), "command_config", nil
	}
	return configured, "command_config", nil
}

func effectiveCommandSpec(spec config.CommandSpec, source string, state project.State) config.CommandSpec {
	if source != "project" || state.Config == nil {
		return spec
	}
	if strings.TrimSpace(spec.RunMode) == "" {
		spec.RunMode = strings.TrimSpace(state.Config.CommandEnv.RunMode)
	}
	if strings.TrimSpace(spec.Shell) == "" {
		spec.Shell = strings.TrimSpace(state.Config.CommandEnv.Shell)
	}
	return spec
}

func commandRunMode(spec config.CommandSpec) string {
	if strings.TrimSpace(spec.RunMode) == "" {
		return "argv"
	}
	return strings.TrimSpace(spec.RunMode)
}

func (r *Runner) runPersistentShell(parentCtx context.Context, spec config.CommandSpec, args []string, cwd, source string, state project.State, extra map[string]any) (any, error) {
	if runtime.GOOS == "windows" {
		return nil, errors.New("persistent_shell run_mode is not supported on Windows")
	}
	if source != "project" || !state.Trusted {
		return nil, fmt.Errorf("command %q run_mode persistent_shell is only allowed for trusted project commands", spec.Name)
	}
	if !r.Cfg.CommandEnvironment.AllowPersistentShell {
		return nil, fmt.Errorf("command %q requires run_mode persistent_shell, but command_environment.allow_persistent_shell is false", spec.Name)
	}
	shellPath := strings.TrimSpace(spec.Shell)
	if shellPath == "" {
		return nil, fmt.Errorf("command %q run_mode persistent_shell requires shell", spec.Name)
	}
	if !r.shellAllowed(shellPath) {
		return nil, fmt.Errorf("command %q shell %q is not allowed by command_environment.allowed_shells", spec.Name, shellPath)
	}
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(r.Cfg.Limits.CommandTimeoutSeconds)*time.Second)
	defer cancel()
	started := time.Now()
	sess, err := r.checkoutPersistentSession(ctx, state.Root, shellPath, cwd, spec)
	if err != nil {
		if isPersistentShellBusy(err) {
			return r.persistentShellBusyResult(started, extra), nil
		}
		return r.persistentShellInitFailureResult(started, err, extra), nil
	}
	extra["shell"] = shellPath
	extra["shell_session"] = sess.key
	extra["shell_pool_size"] = r.persistentShellPoolSize()
	argv := append([]string{spec.Exec}, args...)
	stdout, truncated, exitCode, runErr := sess.runLocked(ctx, cwd, argv, r.Cfg.Limits.MaxCommandOutputBytes)
	timedOut := errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled)
	if shouldRetryPersistentShellRun(ctx, runErr, timedOut) {
		r.dropPersistentSession(sess)
		sess.kill()
		sess.waitAfterKill()
		sess, runErr = r.checkoutPersistentSession(ctx, state.Root, shellPath, cwd, spec)
		if isPersistentShellBusy(runErr) {
			return r.persistentShellBusyResult(started, extra), nil
		}
		if runErr != nil {
			return r.persistentShellInitFailureResult(started, runErr, extra), nil
		}
		extra["shell_session"] = sess.key
		stdout, truncated, exitCode, runErr = sess.runLocked(ctx, cwd, argv, r.Cfg.Limits.MaxCommandOutputBytes)
		timedOut = errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled)
	}
	if runErr != nil {
		r.dropPersistentSession(sess)
		sess.kill()
		sess.waitAfterKill()
		if !timedOut {
			return nil, runErr
		}
	}
	result := map[string]any{
		"exit_code": exitCode, "timed_out": timedOut, "duration_ms": time.Since(started).Milliseconds(), "configured_timeout_seconds": r.Cfg.Limits.CommandTimeoutSeconds,
		"stdout": stripTerminalEscapes(stdout), "stderr": "", "stdout_truncated": truncated, "stderr_truncated": false,
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

func (r *Runner) persistentShellBusyResult(started time.Time, extra map[string]any) map[string]any {
	result := map[string]any{
		"exit_code": -1, "ok": false, "busy": true, "retryable": true, "timed_out": false,
		"duration_ms": time.Since(started).Milliseconds(), "configured_timeout_seconds": r.Cfg.Limits.CommandTimeoutSeconds,
		"stdout": "", "stderr": "", "stdout_truncated": false, "stderr_truncated": false,
		"busy_phase": "persistent_shell_checkout", "message": "persistent shell pool is busy; retry the command later", "shell_pool_size": r.persistentShellPoolSize(),
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

func (r *Runner) persistentShellInitFailureResult(started time.Time, err error, extra map[string]any) map[string]any {
	result := map[string]any{
		"exit_code": -1, "ok": false, "busy": false, "retryable": true,
		"timed_out":   errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled),
		"duration_ms": time.Since(started).Milliseconds(), "configured_timeout_seconds": r.Cfg.Limits.CommandTimeoutSeconds,
		"stdout": "", "stderr": "", "stdout_truncated": false, "stderr_truncated": false,
		"failure_phase": "persistent_shell_init", "error": err.Error(), "shell_pool_size": r.persistentShellPoolSize(),
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

func isPersistentShellBusy(runErr error) bool {
	var busyErr persistentShellBusyError
	return errors.As(runErr, &busyErr)
}

func shouldRetryPersistentShellRun(ctx context.Context, runErr error, timedOut bool) bool {
	if runErr == nil || timedOut || ctx.Err() != nil {
		return false
	}
	var writeErr persistentShellWriteError
	return errors.As(runErr, &writeErr)
}

func (r *Runner) shellAllowed(shellPath string) bool {
	allowed := r.Cfg.CommandEnvironment.AllowedShells
	if len(allowed) == 0 {
		allowed = []string{"/bin/zsh", "/bin/bash", "/bin/sh"}
	}
	for _, candidate := range allowed {
		if shellPath == candidate {
			return true
		}
	}
	return false
}

func (r *Runner) persistentShellPoolSize() int {
	if r.Cfg != nil && r.Cfg.CommandEnvironment.PersistentShellPoolSize > 0 {
		return r.Cfg.CommandEnvironment.PersistentShellPoolSize
	}
	return 2
}

func (r *Runner) persistentShellStartupTimeout() time.Duration {
	if r.Cfg != nil && r.Cfg.CommandEnvironment.PersistentShellStartupTimeoutSeconds > 0 {
		return time.Duration(r.Cfg.CommandEnvironment.PersistentShellStartupTimeoutSeconds) * time.Second
	}
	return 15 * time.Second
}

func (r *Runner) persistentSession(ctx context.Context, root, shellPath, cwd string, spec config.CommandSpec) (*persistentShell, error) {
	sess, err := r.checkoutPersistentSession(ctx, root, shellPath, cwd, spec)
	if err != nil {
		return nil, err
	}
	sess.unlockRun()
	return sess, nil
}

func (r *Runner) persistentPool(key string) *persistentShellPool {
	r.shellMu.Lock()
	defer r.shellMu.Unlock()
	if r.shellPools == nil {
		r.shellPools = map[string]*persistentShellPool{}
	}
	pool := r.shellPools[key]
	if pool == nil {
		pool = &persistentShellPool{key: key}
		r.shellPools[key] = pool
	}
	return pool
}

func (r *Runner) checkoutPersistentSession(ctx context.Context, root, shellPath, cwd string, spec config.CommandSpec) (*persistentShell, error) {
	key := root + "\x00" + shellPath
	pool := r.persistentPool(key)

	pool.mu.Lock()
	for _, sess := range pool.sessions {
		if sess.tryLockRun() {
			pool.mu.Unlock()
			return sess, nil
		}
	}
	if len(pool.sessions)+pool.starting >= r.persistentShellPoolSize() {
		pool.mu.Unlock()
		return nil, persistentShellBusyError{err: errPersistentShellBusy}
	}
	pool.starting++
	pool.mu.Unlock()

	sess, err := newPersistentShell(ctx, r.persistentShellStartupTimeout(), key, shellPath, cwd, spec)
	if err == nil {
		// Claim the newly-created shell before publishing it into the pool so no
		// concurrent foreground command can steal the session from its creator.
		if lockErr := sess.lockRun(ctx); lockErr != nil {
			sess.kill()
			sess.waitAfterKill()
			err = persistentShellBusyError{err: lockErr}
		}
	}

	pool.mu.Lock()
	pool.starting--
	if err == nil {
		pool.sessions = append(pool.sessions, sess)
	}
	pool.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (r *Runner) dropPersistentSession(sess *persistentShell) {
	if sess == nil {
		return
	}
	r.shellMu.Lock()
	pool := r.shellPools[sess.key]
	r.shellMu.Unlock()
	if pool == nil {
		return
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for i, candidate := range pool.sessions {
		if candidate == sess {
			pool.sessions = append(pool.sessions[:i], pool.sessions[i+1:]...)
			break
		}
	}
}

func (r *Runner) finalCommandArgs(spec config.CommandSpec, extra []string, cwd string, state project.State) ([]string, error) {
	if len(extra) > 0 {
		if !spec.AllowExtraArgs {
			return nil, fmt.Errorf("command %q does not allow extra_args", spec.Name)
		}
		maxExtra := effectiveMaxExtraArgs(spec)
		if len(extra) > maxExtra {
			return nil, fmt.Errorf("command %q allows at most %d extra_args", spec.Name, maxExtra)
		}
		for _, arg := range extra {
			if strings.Contains(arg, "\x00") {
				return nil, errors.New("extra_args cannot contain NUL")
			}
			if !r.extraArgAllowed(arg, spec, cwd, state) {
				return nil, fmt.Errorf("extra arg %q is not allowed for command %q", arg, spec.Name)
			}
		}
	}
	args := make([]string, 0, len(spec.Args)+len(extra))
	inserted := false
	for _, arg := range spec.Args {
		if arg == extraArgsPlaceholder {
			args = append(args, extra...)
			inserted = true
			continue
		}
		args = append(args, arg)
	}
	if !inserted {
		args = append(args, extra...)
	}
	return args, nil
}

func effectiveMaxExtraArgs(spec config.CommandSpec) int {
	if spec.MaxExtraArgs > 0 {
		return spec.MaxExtraArgs
	}
	return 10
}

func (r *Runner) extraArgAllowed(arg string, spec config.CommandSpec, cwd string, state project.State) bool {
	if len(spec.ExtraArgs) == 0 {
		return true
	}
	for _, rule := range spec.ExtraArgs {
		switch rule.Kind {
		case "any":
			return true
		case "enum":
			for _, value := range rule.Values {
				if arg == value {
					return true
				}
			}
		case "regex":
			if regexMatch(rule.Pattern, arg) {
				return true
			}
		case "path":
			if strings.HasPrefix(arg, "-") {
				continue
			}
			resolved, err := r.Sandbox.ResolveWithCwd(arg, cwd)
			if err != nil {
				continue
			}
			if rule.MustExist {
				if _, err := os.Stat(resolved); err != nil {
					continue
				}
			}
			if rule.MustBeInsideProject && state.Found {
				rel, err := filepath.Rel(state.Root, resolved)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
					continue
				}
			}
			rel := arg
			if state.Found {
				if candidate, err := filepath.Rel(state.Root, resolved); err == nil {
					rel = filepath.ToSlash(candidate)
				}
			}
			if len(rule.AllowGlobs) == 0 || globAny(rule.AllowGlobs, rel) {
				return true
			}
		}
	}
	return false
}

func regexMatch(pattern, value string) bool {
	if pattern == "" {
		return false
	}
	re, err := regexp.Compile(pattern)
	if err == nil {
		return re.MatchString(value)
	}
	return regexMatchLargeBoundedWholePattern(pattern, value)
}

func regexMatchLargeBoundedWholePattern(pattern, value string) bool {
	class, minCount, maxCount, ok := parseLargeBoundedWholePattern(pattern)
	if !ok {
		return false
	}
	valueLen := len([]rune(value))
	if valueLen < minCount || valueLen > maxCount {
		return false
	}
	re, err := regexp.Compile("^" + class + "+$")
	return err == nil && re.MatchString(value)
}

func parseLargeBoundedWholePattern(pattern string) (class string, minCount, maxCount int, ok bool) {
	if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "}$") {
		return "", 0, 0, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(pattern, "^"), "$")
	open := strings.LastIndex(body, "{")
	comma := strings.LastIndex(body, ",")
	if open <= 0 || comma <= open || !strings.HasSuffix(body, "}") {
		return "", 0, 0, false
	}
	class = body[:open]
	if !strings.HasPrefix(class, "[") || !strings.HasSuffix(class, "]") {
		return "", 0, 0, false
	}
	var err error
	minCount, err = strconv.Atoi(body[open+1 : comma])
	if err != nil || minCount < 0 {
		return "", 0, 0, false
	}
	maxCount, err = strconv.Atoi(body[comma+1 : len(body)-1])
	if err != nil || maxCount < minCount || maxCount <= 1000 {
		return "", 0, 0, false
	}
	return class, minCount, maxCount, true
}

func globAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if ok, _ := filepath.Match(pattern, value); ok {
			return true
		}
		if strings.Contains(pattern, "**") {
			re := regexp.QuoteMeta(filepath.ToSlash(pattern))
			re = strings.ReplaceAll(re, `\*\*`, `.*`)
			re = strings.ReplaceAll(re, `\*`, `[^/]*`)
			if regexMatch("^"+re+"$", value) {
				return true
			}
		}
	}
	return false
}

func (r *Runner) ListNamed(raw json.RawMessage) (any, error) {
	var a struct {
		IncludeArgs bool   `json:"include_args"`
		Cwd         string `json:"cwd"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
	}
	type item struct {
		Name           string   `json:"name"`
		Exec           string   `json:"exec,omitempty"`
		Args           []string `json:"args,omitempty"`
		AllowExtraArgs bool     `json:"allow_extra_args,omitempty"`
		MaxExtraArgs   int      `json:"max_extra_args,omitempty"`
		Description    string   `json:"description,omitempty"`
		RunMode        string   `json:"run_mode,omitempty"`
		Shell          string   `json:"shell,omitempty"`
		Cwd            string   `json:"cwd,omitempty"`
		Source         string   `json:"source"`
	}
	globalItems := make([]item, 0, len(r.Cfg.Commands))
	for i := range r.Cfg.Commands {
		cmd := &r.Cfg.Commands[i]
		it := item{Name: cmd.Name, Description: cmd.Description, Source: "global"}
		if a.IncludeArgs {
			it.Exec = cmd.Exec
			it.Args = append([]string(nil), cmd.Args...)
			it.AllowExtraArgs = cmd.AllowExtraArgs
			if cmd.AllowExtraArgs {
				it.MaxExtraArgs = effectiveMaxExtraArgs(*cmd)
			}
			it.RunMode = commandRunMode(*cmd)
			it.Shell = cmd.Shell
			it.Cwd = cmd.Cwd
		}
		globalItems = append(globalItems, it)
	}
	out := map[string]any{"global_commands": globalItems, "count": len(globalItems)}
	if r.Projects != nil && strings.TrimSpace(a.Cwd) != "" {
		state := r.Projects.Discover(a.Cwd)
		out["project"] = state
		if state.Found && state.Config != nil {
			projectItems := make([]item, 0, len(state.Config.Commands))
			for i := range state.Config.Commands {
				cmd := &state.Config.Commands[i]
				effective := effectiveCommandSpec(*cmd, "project", state)
				it := item{Name: effective.Name, Description: effective.Description, Source: "project"}
				if a.IncludeArgs {
					it.Exec = effective.Exec
					it.Args = append([]string(nil), effective.Args...)
					it.AllowExtraArgs = effective.AllowExtraArgs
					if effective.AllowExtraArgs {
						it.MaxExtraArgs = effectiveMaxExtraArgs(effective)
					}
					it.RunMode = commandRunMode(effective)
					it.Shell = effective.Shell
					it.Cwd = effective.Cwd
				}
				projectItems = append(projectItems, it)
			}
			out["project_commands"] = projectItems
		}
	}
	return out, nil
}

func (r *Runner) lookupSequence(name, cwd string) (config.CommandSequenceSpec, string, project.State, bool) {
	var state project.State
	if r.Projects != nil && strings.TrimSpace(cwd) != "" {
		state = r.Projects.Discover(cwd)
		if state.Found && state.Trusted && state.Config != nil {
			for i := range state.Config.CommandSequences {
				seq := &state.Config.CommandSequences[i]
				if seq.Name == name {
					return *seq, "project", state, true
				}
			}
		}
	}
	for i := range r.Cfg.CommandSequences {
		seq := &r.Cfg.CommandSequences[i]
		if seq.Name == name {
			return *seq, "global", state, true
		}
	}
	return config.CommandSequenceSpec{}, "", state, false
}

func (r *Runner) lookupNamed(name, cwd string) (config.CommandSpec, string, project.State, bool) {
	var state project.State
	if r.Projects != nil && strings.TrimSpace(cwd) != "" {
		state = r.Projects.Discover(cwd)
		if state.Found && state.Trusted && state.Config != nil {
			for i := range state.Config.Commands {
				cmd := &state.Config.Commands[i]
				if cmd.Name == name {
					return effectiveCommandSpec(*cmd, "project", state), "project", state, true
				}
			}
		}
	}
	spec, ok := r.Specs[name]
	return spec, "global", state, ok
}

func (r *Runner) availableCommandNames(cwd string) []string {
	seen := map[string]bool{}
	var names []string
	for i := range r.Cfg.Commands {
		cmd := &r.Cfg.Commands[i]
		if !seen[cmd.Name] {
			seen[cmd.Name] = true
			names = append(names, cmd.Name)
		}
	}
	if r.Projects != nil && strings.TrimSpace(cwd) != "" {
		state := r.Projects.Discover(cwd)
		if state.Found && state.Trusted && state.Config != nil {
			for i := range state.Config.Commands {
				cmd := &state.Config.Commands[i]
				if !seen[cmd.Name] {
					seen[cmd.Name] = true
					names = append(names, cmd.Name)
				}
			}
		}
	}
	return names
}

type RunArgvArgs struct {
	Exec string   `json:"exec"`
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

func (r *Runner) RunArgv(raw json.RawMessage) (any, error) {
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
		// continue
	case policy.ActionDeny:
		return nil, fmt.Errorf("command policy denied %s %s: %s", a.Exec, policy.JoinArgs(a.Args), decision.Rule)
	case policy.ActionPrompt:
		if !r.Cfg.Approval.Enabled || r.Approver == nil {
			return nil, fmt.Errorf("command policy requires approval for %s %s, but approval is disabled", a.Exec, policy.JoinArgs(a.Args))
		}
		_, err := r.Approver.Request(context.Background(), approval.Request{
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
	return r.runExec(context.Background(), a.Exec, a.Args, cwd, map[string]string{}, nil, map[string]any{"exec": a.Exec, "args": a.Args, "cwd": a.Cwd, "policy_action": decision.Action, "policy_rule": decision.Rule})
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

// ClosePersistentShells terminates any cached persistent shell sessions owned by the runner.
func (r *Runner) ClosePersistentShells() {
	r.shellMu.Lock()
	pools := make([]*persistentShellPool, 0, len(r.shellPools))
	for _, pool := range r.shellPools {
		pools = append(pools, pool)
	}
	r.shellPools = map[string]*persistentShellPool{}
	r.shellMu.Unlock()

	var sessions []*persistentShell
	for _, pool := range pools {
		pool.mu.Lock()
		sessions = append(sessions, pool.sessions...)
		pool.sessions = nil
		pool.starting = 0
		pool.mu.Unlock()
	}
	for _, sess := range sessions {
		sess.kill()
		sess.waitAfterKill()
	}
}
