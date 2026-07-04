package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileCreatesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("write second: %v", err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test-owned temp file.
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "second" {
		t.Fatalf("content = %q", string(b))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o", got)
	}
}
