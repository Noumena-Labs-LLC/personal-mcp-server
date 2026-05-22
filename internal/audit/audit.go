package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type Logger struct {
	l          *log.Logger
	file       *os.File
	path       string
	maxBytes   int64
	maxBackups int
	sizeBytes  int64
	queue      chan []byte
	closeCh    chan struct{}
	done       chan struct{}
	closed     atomic.Bool
	dropped    atomic.Uint64
	closeErr   atomic.Value
}

func New(path string, maxBytes int64, maxBackups int) (*Logger, error) {
	if path == "" {
		logger := &Logger{l: log.New(os.Stderr, "audit ", 0), queue: make(chan []byte, 1024), closeCh: make(chan struct{}), done: make(chan struct{})}
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
	logger := &Logger{l: log.New(f, "", 0), file: f, path: path, maxBytes: maxBytes, maxBackups: maxBackups, sizeBytes: sizeBytes, queue: make(chan []byte, 1024), closeCh: make(chan struct{}), done: make(chan struct{})}
	go logger.writeLoop()
	return logger, nil
}

func (a *Logger) writeLoop() {
	defer close(a.done)
	for {
		select {
		case b := <-a.queue:
			a.writeEventBytes(b)
		case <-a.closeCh:
			a.drainQueue()
			a.closeFile()
			return
		}
	}
}

func (a *Logger) drainQueue() {
	for {
		select {
		case b := <-a.queue:
			a.writeEventBytes(b)
		default:
			return
		}
	}
}

func (a *Logger) Event(tool string, fields map[string]any) {
	if a == nil || a.closed.Load() {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["tool"] = tool
	b, _ := json.Marshal(fields)
	if a.closed.Load() {
		return
	}
	select {
	case a.queue <- b:
	default:
		a.dropped.Add(1)
	}
}

func (a *Logger) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return a.dropped.Load()
}

func (a *Logger) writeEventBytes(b []byte) {
	incoming := int64(len(b) + 1)
	_ = a.rotateIfNeeded(incoming)
	a.l.Println(string(b))
	a.sizeBytes += incoming
}

func (a *Logger) Close() error {
	if a == nil {
		return nil
	}
	if a.closed.CompareAndSwap(false, true) {
		close(a.closeCh)
	}
	if a.done != nil {
		<-a.done
	}
	if v := a.closeErr.Load(); v != nil {
		if err, ok := v.(error); ok {
			return err
		}
		return fmt.Errorf("unexpected stored audit close error type %T", v)
	}
	return nil
}

func (a *Logger) closeFile() {
	if a.file == nil {
		return
	}
	if err := a.file.Close(); err != nil {
		a.closeErr.Store(err)
	}
	a.file = nil
	a.l.SetOutput(io.Discard)
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
