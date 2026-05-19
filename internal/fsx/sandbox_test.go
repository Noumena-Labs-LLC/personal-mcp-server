package fsx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func testConfig(root string) *config.Config {
	return &config.Config{
		Roots:      []string{root},
		Limits:     config.LimitsConfig{MaxReadBytes: 1024, MaxWriteBytes: 1024, MaxSearchResults: 10, MaxSearchFileBytes: 1024, CommandTimeoutSeconds: 1, MaxCommandOutputBytes: 1024},
		Secrets:    config.SecretsConfig{DenyNames: []string{".env", "id_rsa"}, DenyExtensions: []string{".pem", ".key"}},
		FilePolicy: config.FilePolicyConfig{ReadDefault: "allow", WriteDefault: "allow", CreateDefault: "allow", PatchDefault: "allow"},
	}
}

func TestSandboxRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	s := NewSandbox(testConfig(root))
	if _, err := s.Resolve("../outside.txt"); err == nil {
		t.Fatal("expected traversal outside root to be rejected")
	}
}

func TestSandboxRejectsSecretName(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSandbox(testConfig(root))
	if _, err := s.Resolve(".env"); err == nil {
		t.Fatal("expected denied secret filename to be rejected")
	}
}

func TestSandboxRejectsSymlinkOutsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is environment-dependent on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	s := NewSandbox(testConfig(root))
	if _, err := s.Resolve("link.txt"); err == nil {
		t.Fatal("expected symlink outside root to be rejected")
	}
}

func TestSandboxRejectsSymlinkChainOutsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is environment-dependent on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "link1"), filepath.Join(root, "link2")); err != nil {
		t.Fatal(err)
	}
	s := NewSandbox(testConfig(root))
	if _, err := s.Resolve("link2"); err == nil {
		t.Fatal("expected symlink chain outside root to be rejected")
	}
}

func TestSandboxRootFor(t *testing.T) {
	root := t.TempDir()
	s := NewSandbox(testConfig(root))
	got, ok := s.RootFor(filepath.Join(root, "a", "b.txt"))
	if !ok {
		t.Fatalf("expected root match for path inside root")
	}
	if len(s.Roots) != 1 {
		t.Fatalf("expected one normalized root, got %d", len(s.Roots))
	}
	if got != s.Roots[0] {
		t.Fatalf("expected stored normalized root %q, got %q", s.Roots[0], got)
	}
}

func TestSandboxAbsolutePathInsideAndOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	insideFile := filepath.Join(root, "inside.txt")
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(insideFile, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideFile, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSandbox(testConfig(root))
	if _, err := s.Resolve(insideFile); err != nil {
		t.Fatalf("expected absolute path inside root to resolve: %v", err)
	}
	if _, err := s.Resolve(outsideFile); err == nil {
		t.Fatalf("expected absolute path outside root to be rejected")
	}
}

func TestSandboxResolveWithCwd(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, "sub", "file.txt")
	if err := os.WriteFile(file, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSandbox(testConfig(root))
	resolved, err := s.ResolveWithCwd("sub/file.txt", "project")
	if err != nil {
		t.Fatalf("expected cwd-relative path to resolve: %v", err)
	}
	if resolved != canonicalPathForCheck(file) {
		t.Fatalf("expected %q, got %q", canonicalPathForCheck(file), resolved)
	}
	if _, err := s.ResolveWithCwd("../../outside.txt", "project"); err == nil {
		t.Fatal("expected cwd-relative traversal outside root to be rejected")
	}
}
