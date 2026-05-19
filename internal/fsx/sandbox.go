package fsx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

type Sandbox struct {
	Roots          []string
	DenyNames      map[string]bool
	DenyExtensions map[string]bool
}

func NewSandbox(c *config.Config) *Sandbox {
	s := &Sandbox{
		Roots:          canonicalRoots(c.Roots),
		DenyNames:      map[string]bool{},
		DenyExtensions: map[string]bool{},
	}
	for _, n := range c.Secrets.DenyNames {
		s.DenyNames[strings.ToLower(n)] = true
	}
	for _, ext := range c.Secrets.DenyExtensions {
		s.DenyExtensions[strings.ToLower(ext)] = true
	}
	return s
}

func canonicalRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		clean := filepath.Clean(root)
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			clean = resolved
		}
		out = append(out, clean)
	}
	return out
}

func (s *Sandbox) Resolve(userPath string) (string, error) {
	if userPath == "" {
		userPath = "."
	}
	if strings.Contains(userPath, "\x00") {
		return "", errors.New("path contains NUL")
	}
	var candidates []string
	if filepath.IsAbs(userPath) {
		candidates = []string{filepath.Clean(userPath)}
	} else {
		for _, root := range s.Roots {
			candidates = append(candidates, filepath.Join(root, userPath))
		}
	}
	for _, cand := range candidates {
		resolved, err := resolveExistingOrParent(cand)
		if err != nil {
			continue
		}
		if s.InRoot(resolved) {
			if s.IsDenied(resolved) {
				return "", fmt.Errorf("refusing denied file path %q", userPath)
			}
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowed roots", userPath)
}

// ResolveWithCwd resolves userPath inside the sandbox. If cwd is provided and
// userPath is relative, cwd is resolved first and used as the base directory.
// This never changes the process working directory.
func (s *Sandbox) ResolveWithCwd(userPath, cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return s.Resolve(userPath)
	}
	base, err := s.Resolve(cwd)
	if err != nil {
		return "", fmt.Errorf("cwd %q: %w", cwd, err)
	}
	info, err := os.Stat(base)
	if err != nil {
		return "", fmt.Errorf("cwd %q: %w", cwd, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", cwd)
	}
	if userPath == "" {
		userPath = "."
	}
	if filepath.IsAbs(userPath) {
		return s.Resolve(userPath)
	}
	return s.Resolve(filepath.Join(base, userPath))
}

// ResolveWithMode resolves a path using an explicit path interpretation mode.
// Modes:
//
//	auto: absolute paths are absolute; relative paths use cwd when supplied, otherwise roots.
//	absolute: userPath must be an absolute filesystem path.
//	root_relative: userPath is interpreted relative to configured roots.
//	cwd_relative: userPath is interpreted relative to cwd.
func (s *Sandbox) ResolveWithMode(userPath, cwd, pathMode string) (string, error) {
	mode := strings.TrimSpace(pathMode)
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto":
		return s.ResolveWithCwd(userPath, cwd)
	case "absolute":
		if !filepath.IsAbs(userPath) {
			return "", fmt.Errorf("path_mode=absolute requires an absolute path, got %q", userPath)
		}
		return s.Resolve(userPath)
	case "root_relative":
		if filepath.IsAbs(userPath) {
			return "", fmt.Errorf("path_mode=root_relative requires a relative path, got %q", userPath)
		}
		return s.Resolve(userPath)
	case "cwd_relative":
		if strings.TrimSpace(cwd) == "" {
			return "", errors.New("path_mode=cwd_relative requires cwd")
		}
		if filepath.IsAbs(userPath) {
			return "", fmt.Errorf("path_mode=cwd_relative requires a relative path, got %q", userPath)
		}
		return s.ResolveWithCwd(userPath, cwd)
	default:
		return "", fmt.Errorf("invalid path_mode %q; use auto, absolute, root_relative, or cwd_relative", pathMode)
	}
}

// DisplayPathWithCwd returns a stable display path for audit and policy
// matching. Enforcement always uses resolved absolute paths; this is only for
// human-readable details and user-authored regex policies.
func DisplayPathWithCwd(userPath, cwd string) string {
	if strings.TrimSpace(userPath) == "" {
		userPath = "."
	}
	if strings.TrimSpace(cwd) == "" || filepath.IsAbs(userPath) {
		return filepath.Clean(userPath)
	}
	return filepath.Clean(filepath.Join(cwd, userPath))
}

func (s *Sandbox) InRoot(path string) bool {
	path = canonicalPathForCheck(path)
	for _, root := range s.Roots {
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func (s *Sandbox) RootFor(path string) (string, bool) {
	path = canonicalPathForCheck(path)
	for _, root := range s.Roots {
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return root, true
		}
	}
	return "", false
}

func canonicalPathForCheck(path string) string {
	if resolved, err := resolveExistingOrParent(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func (s *Sandbox) IsDenied(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if s.DenyNames[base] {
		return true
	}
	return s.DenyExtensions[strings.ToLower(filepath.Ext(path))]
}

func resolveExistingOrParent(path string) (string, error) {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved, nil
	}
	parts := []string{}
	cur := clean
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(parts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, parts[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", os.ErrNotExist
		}
		parts = append(parts, filepath.Base(cur))
		cur = parent
	}
}
