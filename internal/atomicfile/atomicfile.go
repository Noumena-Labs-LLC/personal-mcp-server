package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile atomically replaces path with data using a same-directory temp file.
// It fsyncs both the temp file and parent directory before returning when the
// platform supports directory fsync.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync parent dir: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- caller supplies local config/service directory.
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
