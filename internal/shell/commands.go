package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
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
