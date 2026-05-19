package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatesAuditLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := New(path, 60, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		l.Event("tool", map[string]any{"message": "this message is long enough to rotate"})
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close audit logger: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
}
