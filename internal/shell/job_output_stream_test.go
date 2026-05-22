package shell

import (
	"strings"
	"testing"
	"time"
)

func testJobOutputStream() *jobOutputStream {
	return newJobOutputStream(jobOutputLimits{MaxLines: 3, MaxLineBytes: 8, MaxTailBytes: 64, ChannelSize: 4})
}

func waitForOutputTail(t *testing.T, s *jobOutputStream, want string) outputTailResult {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var tail outputTailResult
	for time.Now().Before(deadline) {
		tail = s.Tail(10)
		if strings.Contains(tail.Text, want) {
			return tail
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("output tail never contained %q; last tail %#v", want, tail)
	return outputTailResult{}
}

func TestJobOutputStreamAssemblesLinesAcrossChunks(t *testing.T) {
	s := testJobOutputStream()
	if _, err := s.Write([]byte("hel")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Write([]byte("lo\nwor")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Write([]byte("ld\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.CloseAndFlush()

	tail := s.Tail(10)
	if tail.Text != "hello\nworld" {
		t.Fatalf("unexpected tail %q", tail.Text)
	}
	if tail.LineShortRead || tail.LinesTruncated || tail.TailShortRead {
		t.Fatalf("unexpected truncation flags: %#v", tail)
	}
}

func TestJobOutputStreamPublishesCurrentPartialLine(t *testing.T) {
	s := testJobOutputStream()
	if _, err := s.Write([]byte("partial")); err != nil {
		t.Fatalf("write: %v", err)
	}
	tail := waitForOutputTail(t, s, "partial")
	if tail.Text != "partial" {
		t.Fatalf("expected partial line in snapshot, got %q", tail.Text)
	}
	s.CloseAndFlush()
}

func TestJobOutputStreamFlushesPartialLineOnClose(t *testing.T) {
	s := testJobOutputStream()
	if _, err := s.Write([]byte("no-newline")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.CloseAndFlush()

	tail := s.Tail(10)
	if tail.Text != "-newline" {
		t.Fatalf("expected flushed shortened partial line, got %q", tail.Text)
	}
	if !tail.LineShortRead {
		t.Fatalf("expected per-line short read for overlong line: %#v", tail)
	}
}

func TestJobOutputStreamRingEvictsOldLines(t *testing.T) {
	s := testJobOutputStream()
	if _, err := s.Write([]byte("one\ntwo\nthree\nfour\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.CloseAndFlush()

	tail := s.Tail(10)
	if tail.Text != "two\nthree\nfour" {
		t.Fatalf("expected ring to keep newest lines, got %q", tail.Text)
	}
	if !tail.LinesTruncated {
		t.Fatalf("expected ring truncation flag: %#v", tail)
	}
}

func TestJobOutputStreamTailLines(t *testing.T) {
	s := testJobOutputStream()
	if _, err := s.Write([]byte("one\ntwo\nthree\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.CloseAndFlush()

	tail := s.Tail(2)
	if tail.Text != "two\nthree" {
		t.Fatalf("expected last two lines, got %q", tail.Text)
	}
}

func TestJobOutputStreamPerLineByteLimitKeepsSuffix(t *testing.T) {
	s := testJobOutputStream()
	if _, err := s.Write([]byte("abcdefghijklmnopqrstuvwxyz\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.CloseAndFlush()

	tail := s.Tail(10)
	if tail.Text != "stuvwxyz" {
		t.Fatalf("expected suffix of long line, got %q", tail.Text)
	}
	if !tail.LineShortRead {
		t.Fatalf("expected line short-read flag: %#v", tail)
	}
}

func TestJobOutputStreamTailByteLimitKeepsSuffix(t *testing.T) {
	s := newJobOutputStream(jobOutputLimits{MaxLines: 10, MaxLineBytes: 100, MaxTailBytes: 7, ChannelSize: 4})
	if _, err := s.Write([]byte("alpha\nbeta\ngamma\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.CloseAndFlush()

	tail := s.Tail(10)
	if tail.Text != "a\ngamma" {
		t.Fatalf("expected suffix-constrained tail, got %q", tail.Text)
	}
	if !tail.TailShortRead {
		t.Fatalf("expected tail short-read flag: %#v", tail)
	}
}

func TestJobOutputStreamWriteAfterCloseIsIgnored(t *testing.T) {
	s := testJobOutputStream()
	if _, err := s.Write([]byte("before\n")); err != nil {
		t.Fatalf("write before close: %v", err)
	}
	s.CloseAndFlush()
	if _, err := s.Write([]byte("after\n")); err != nil {
		t.Fatalf("write after close: %v", err)
	}

	tail := s.Tail(10)
	if strings.Contains(tail.Text, "after") {
		t.Fatalf("write after close should be ignored, got %q", tail.Text)
	}
}
