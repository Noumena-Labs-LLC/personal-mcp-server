package audit

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/logwriter"
)

type Logger struct {
	l      *log.Logger
	writer *logwriter.Writer
}

func New(path string, maxBytes int64, maxBackups int) (*Logger, error) {
	if path == "" {
		writer := logwriter.New(os.Stderr)
		return &Logger{l: log.New(writer, "audit ", 0), writer: writer}, nil
	}
	writer, err := logwriter.NewRotatingFile(path, maxBytes, maxBackups)
	if err != nil {
		return nil, err
	}
	return &Logger{l: log.New(writer, "", 0), writer: writer}, nil
}

func (a *Logger) Event(tool string, fields map[string]any) {
	if a == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["tool"] = tool
	b, _ := json.Marshal(fields)
	_ = a.l.Output(2, string(b))
}

func (a *Logger) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return a.writer.Dropped()
}

func (a *Logger) Close() error {
	if a == nil {
		return nil
	}
	return a.writer.Close()
}
