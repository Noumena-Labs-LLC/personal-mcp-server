package logwriter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingFileRotatesNumberedBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	writer, err := NewRotatingFile(path, 20, 2)
	if err != nil {
		t.Fatalf("new rotating file writer: %v", err)
	}

	for _, line := range []string{"aaaaaaaaaa\n", "bbbbbbbbbb\n", "cccccccccc\n"} {
		if _, err := writer.Write([]byte(line)); err != nil {
			t.Fatalf("write %q: %v", line, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	active, err := os.ReadFile(path) //nolint:gosec // path is a test-owned temp log file.
	if err != nil {
		t.Fatalf("read active log: %v", err)
	}
	if string(active) != "cccccccccc\n" {
		t.Fatalf("active log = %q, want final line", string(active))
	}
	firstBackup, err := os.ReadFile(path + ".1") //nolint:gosec // path is a test-owned temp log file.
	if err != nil {
		t.Fatalf("read first backup: %v", err)
	}
	if string(firstBackup) != "bbbbbbbbbb\n" {
		t.Fatalf("first backup = %q, want second line", string(firstBackup))
	}
	secondBackup, err := os.ReadFile(path + ".2") //nolint:gosec // path is a test-owned temp log file.
	if err != nil {
		t.Fatalf("read second backup: %v", err)
	}
	if string(secondBackup) != "aaaaaaaaaa\n" {
		t.Fatalf("second backup = %q, want first line", string(secondBackup))
	}
}
