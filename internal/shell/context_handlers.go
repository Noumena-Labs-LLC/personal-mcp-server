package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

func (r *Runner) RunNamedContext(ctx context.Context, raw json.RawMessage) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return r.runNamed(ctx, raw)
}

func (r *Runner) RunArgvContext(ctx context.Context, raw json.RawMessage) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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
