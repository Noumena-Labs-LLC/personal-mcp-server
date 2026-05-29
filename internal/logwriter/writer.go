package logwriter

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
)

const (
	defaultMaxBytes = 10 * 1024 * 1024
	defaultQueueLen = 1024
)

// Writer asynchronously writes queued log entries to an underlying sink.
type Writer struct {
	sink     io.WriteCloser
	queue    chan []byte
	closeCh  chan struct{}
	done     chan struct{}
	closed   atomic.Bool
	dropped  atomic.Uint64
	closeErr atomic.Value
}

func New(sink io.Writer) *Writer {
	return newWriter(nopWriteCloser{Writer: sink})
}

func NewRotatingFile(path string, maxBytes int64, maxBackups int) (*Writer, error) {
	sink, err := newRotatingFile(path, maxBytes, maxBackups)
	if err != nil {
		return nil, err
	}
	return newWriter(sink), nil
}

func newWriter(sink io.WriteCloser) *Writer {
	w := &Writer{
		sink:    sink,
		queue:   make(chan []byte, defaultQueueLen),
		closeCh: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go w.writeLoop()
	return w
}

func (w *Writer) Write(p []byte) (int, error) {
	if w == nil || w.closed.Load() {
		return 0, os.ErrClosed
	}
	entry := append([]byte(nil), p...)
	if w.closed.Load() {
		return 0, os.ErrClosed
	}
	select {
	case w.queue <- entry:
	default:
		w.dropped.Add(1)
	}
	return len(p), nil
}

func (w *Writer) Dropped() uint64 {
	if w == nil {
		return 0
	}
	return w.dropped.Load()
}

func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	if w.closed.CompareAndSwap(false, true) {
		close(w.closeCh)
	}
	if w.done != nil {
		<-w.done
	}
	if v := w.closeErr.Load(); v != nil {
		if err, ok := v.(error); ok {
			return err
		}
		return fmt.Errorf("unexpected stored log writer close error type %T", v)
	}
	return nil
}

func (w *Writer) writeLoop() {
	defer close(w.done)
	for {
		select {
		case p := <-w.queue:
			_, _ = w.sink.Write(p)
		case <-w.closeCh:
			w.drainQueue()
			if err := w.sink.Close(); err != nil {
				w.closeErr.Store(err)
			}
			return
		}
	}
}

func (w *Writer) drainQueue() {
	for {
		select {
		case p := <-w.queue:
			_, _ = w.sink.Write(p)
		default:
			return
		}
	}
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error {
	return nil
}

type rotatingFile struct {
	file       *os.File
	path       string
	maxBytes   int64
	maxBackups int
	sizeBytes  int64
}

func newRotatingFile(path string, maxBytes int64, maxBackups int) (*rotatingFile, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	if maxBackups < 0 {
		maxBackups = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // log path comes from trusted local config or CLI.
	if err != nil {
		return nil, err
	}
	sizeBytes := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		sizeBytes = info.Size()
	}
	return &rotatingFile{file: f, path: path, maxBytes: maxBytes, maxBackups: maxBackups, sizeBytes: sizeBytes}, nil
}

func (f *rotatingFile) Write(p []byte) (int, error) {
	if err := f.rotateIfNeeded(int64(len(p))); err != nil {
		return 0, err
	}
	n, err := f.file.Write(p)
	f.sizeBytes += int64(n)
	return n, err
}

func (f *rotatingFile) Close() error {
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

func (f *rotatingFile) rotateIfNeeded(incoming int64) error {
	if f.file == nil || f.path == "" || f.maxBytes <= 0 {
		return nil
	}
	if f.sizeBytes+incoming <= f.maxBytes {
		return nil
	}
	if err := f.file.Close(); err != nil {
		return err
	}
	if f.maxBackups > 0 {
		oldest := fmt.Sprintf("%s.%d", f.path, f.maxBackups)
		_ = os.Remove(oldest)
		for i := f.maxBackups - 1; i >= 1; i-- {
			old := fmt.Sprintf("%s.%d", f.path, i)
			rotated := fmt.Sprintf("%s.%d", f.path, i+1)
			_ = os.Rename(old, rotated)
		}
		_ = os.Rename(f.path, f.path+".1")
	} else {
		_ = os.Remove(f.path)
	}
	next, err := os.OpenFile(f.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // log path comes from trusted local config or CLI.
	if err != nil {
		return err
	}
	f.file = next
	f.sizeBytes = 0
	return nil
}
