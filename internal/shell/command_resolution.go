package shell

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
)

const extraArgsPlaceholder = "{{extra_args}}"

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
	if len(spec.StartupFiles) == 0 {
		spec.StartupFiles = append([]string(nil), state.Config.CommandEnv.StartupFiles...)
	}
	return spec
}

func commandRunMode(spec config.CommandSpec) string {
	if strings.TrimSpace(spec.RunMode) == "" {
		return "argv"
	}
	return strings.TrimSpace(spec.RunMode)
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
