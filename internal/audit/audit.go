package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type Logger struct {
	mu         sync.Mutex
	l          *log.Logger
	file       *os.File
	path       string
	maxBytes   int64
	maxBackups int
	sizeBytes  int64
	queue      chan []byte
	done       chan struct{}
	closed     bool
	dropped    atomic.Uint64
}

func New(path string, maxBytes int64, maxBackups int) (*Logger, error) {
	if path == "" {
		logger := &Logger{l: log.New(os.Stderr, "audit ", 0), queue: make(chan []byte, 1024), done: make(chan struct{})}
		go logger.writeLoop()
		return logger, nil
	}
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	if maxBackups < 0 {
		maxBackups = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // audit path comes from trusted local config or CLI.
	if err != nil {
		return nil, err
	}
	sizeBytes := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		sizeBytes = info.Size()
	}
	logger := &Logger{l: log.New(f, "", 0), file: f, path: path, maxBytes: maxBytes, maxBackups: maxBackups, sizeBytes: sizeBytes, queue: make(chan []byte, 1024), done: make(chan struct{})}
	go logger.writeLoop()
	return logger, nil
}

func (a *Logger) writeLoop() {
	defer close(a.done)
	for b := range a.queue {
		a.writeEventBytes(b)
	}
}

func (a *Logger) Event(tool string, fields map[string]any) {
	if a == nil || a.l == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["tool"] = tool
	b, _ := json.Marshal(fields)
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	select {
	case a.queue <- b:
	default:
		a.dropped.Add(1)
	}
	a.mu.Unlock()
}

func (a *Logger) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return a.dropped.Load()
}

func (a *Logger) writeEventBytes(b []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	incoming := int64(len(b) + 1)
	_ = a.rotateIfNeeded(incoming)
	a.l.Println(string(b))
	a.sizeBytes += incoming
}

func (a *Logger) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	if a.queue != nil {
		close(a.queue)
	}
	done := a.done
	a.mu.Unlock()
	if done != nil {
		<-done
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file == nil {
		return nil
	}
	err := a.file.Close()
	a.file = nil
	a.l.SetOutput(io.Discard)
	return err
}

func (a *Logger) rotateIfNeeded(incoming int64) error {
	if a.file == nil || a.path == "" || a.maxBytes <= 0 {
		return nil
	}
	if a.sizeBytes+incoming <= a.maxBytes {
		return nil
	}
	_ = a.file.Close()
	if a.maxBackups > 0 {
		oldest := fmt.Sprintf("%s.%d", a.path, a.maxBackups)
		_ = os.Remove(oldest)
		for i := a.maxBackups - 1; i >= 1; i-- {
			old := fmt.Sprintf("%s.%d", a.path, i)
			rotated := fmt.Sprintf("%s.%d", a.path, i+1)
			_ = os.Rename(old, rotated)
		}
		_ = os.Rename(a.path, a.path+".1")
	} else {
		_ = os.Remove(a.path)
	}
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // audit path comes from trusted local config or CLI.
	if err != nil {
		return err
	}
	a.file = f
	a.sizeBytes = 0
	a.l.SetOutput(f)
	return nil
}