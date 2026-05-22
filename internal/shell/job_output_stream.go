package shell

import (
	"strings"
	"sync"
	"sync/atomic"
)

type jobOutputLimits struct {
	MaxLines     int
	MaxLineBytes int
	MaxTailBytes int
	ChannelSize  int
}

type outputLine struct {
	Text      string
	ShortRead bool
}

type outputSnapshot struct {
	Lines          []outputLine
	Partial        outputLine
	HasPartial     bool
	LinesTruncated bool
	LineShortRead  bool
}

type outputTailResult struct {
	Text           string
	TailLines      int
	LinesTruncated bool
	LineShortRead  bool
	TailShortRead  bool
	DroppedChunks  uint64
	Available      bool
}

type jobOutputMessage struct {
	data      []byte
	closeDone chan struct{}
}

type jobOutputStream struct {
	ch       chan jobOutputMessage
	limits   jobOutputLimits
	dropped  atomic.Uint64
	closed   atomic.Bool
	snapshot atomic.Value // outputSnapshot
	closeMu  sync.Mutex
}

func newJobOutputStream(limits jobOutputLimits) *jobOutputStream {
	limits = normalizeJobOutputLimits(limits)
	s := &jobOutputStream{
		ch:     make(chan jobOutputMessage, limits.ChannelSize),
		limits: limits,
	}
	s.snapshot.Store(outputSnapshot{})
	go s.run()
	return s
}

func normalizeJobOutputLimits(limits jobOutputLimits) jobOutputLimits {
	if limits.MaxLines <= 0 {
		limits.MaxLines = defaultJobOutputLines
	}
	if limits.MaxLineBytes <= 0 {
		limits.MaxLineBytes = 8192
	}
	if limits.MaxTailBytes <= 0 {
		limits.MaxTailBytes = 65536
	}
	if limits.ChannelSize <= 0 {
		limits.ChannelSize = jobOutputChannelSize
	}
	return limits
}

func (s *jobOutputStream) Write(p []byte) (int, error) {
	if len(p) == 0 || s == nil || s.closed.Load() {
		return len(p), nil
	}
	chunk := append([]byte(nil), p...)
	select {
	case s.ch <- jobOutputMessage{data: chunk}:
	default:
		s.dropped.Add(1)
	}
	return len(p), nil
}

func (s *jobOutputStream) CloseAndFlush() {
	if s == nil {
		return
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed.Swap(true) {
		return
	}
	done := make(chan struct{})
	s.ch <- jobOutputMessage{closeDone: done}
	<-done
}

func (s *jobOutputStream) Tail(tailLines int) outputTailResult {
	if s == nil {
		return outputTailResult{TailLines: tailLines}
	}
	if tailLines <= 0 {
		tailLines = defaultJobOutputTailLines
	}
	snap, _ := s.snapshot.Load().(outputSnapshot)
	return tailOutputSnapshot(snap, tailLines, s.limits.MaxTailBytes, s.dropped.Load())
}

func (s *jobOutputStream) run() {
	ring := newOutputLineRing(s.limits.MaxLines)
	partial := make([]byte, 0)
	partialShort := false
	for msg := range s.ch {
		if msg.closeDone != nil {
			if len(partial) > 0 || partialShort {
				ring.add(outputLine{Text: string(partial), ShortRead: partialShort})
				partial = partial[:0]
				partialShort = false
			}
			s.publish(ring, partial, partialShort)
			close(msg.closeDone)
			return
		}
		partial, partialShort = s.processChunk(ring, partial, partialShort, msg.data)
		s.publish(ring, partial, partialShort)
	}
}

func (s *jobOutputStream) processChunk(ring *outputLineRing, partial []byte, partialShort bool, chunk []byte) ([]byte, bool) {
	for len(chunk) > 0 {
		idx := indexByte(chunk, '\n')
		if idx < 0 {
			return appendLimitedBytes(partial, chunk, s.limits.MaxLineBytes, partialShort)
		}
		var short bool
		partial, short = appendLimitedBytes(partial, chunk[:idx], s.limits.MaxLineBytes, partialShort)
		ring.add(outputLine{Text: string(partial), ShortRead: short})
		partial = partial[:0]
		partialShort = false
		chunk = chunk[idx+1:]
	}
	return partial, partialShort
}

func (s *jobOutputStream) publish(ring *outputLineRing, partial []byte, partialShort bool) {
	snap := ring.snapshot()
	if len(partial) > 0 || partialShort {
		snap.Partial = outputLine{Text: string(partial), ShortRead: partialShort}
		snap.HasPartial = true
		if partialShort {
			snap.LineShortRead = true
		}
	}
	s.snapshot.Store(snap)
}

func appendLimitedBytes(dst, src []byte, limit int, alreadyShort bool) ([]byte, bool) {
	if limit <= 0 {
		return dst[:0], alreadyShort || len(src) > 0
	}
	if len(src) >= limit {
		dst = dst[:0]
		dst = append(dst, src[len(src)-limit:]...)
		return dst, true
	}
	dst = append(dst, src...)
	if len(dst) <= limit {
		return dst, alreadyShort
	}
	copy(dst, dst[len(dst)-limit:])
	return dst[:limit], true
}

func indexByte(b []byte, c byte) int {
	for i, value := range b {
		if value == c {
			return i
		}
	}
	return -1
}

type outputLineRing struct {
	lines         []outputLine
	next          int
	count         int
	truncated     bool
	lineShortRead bool
}

func newOutputLineRing(maxLines int) *outputLineRing {
	if maxLines <= 0 {
		maxLines = defaultJobOutputLines
	}
	return &outputLineRing{lines: make([]outputLine, maxLines)}
}

func (r *outputLineRing) add(line outputLine) {
	if len(r.lines) == 0 {
		r.truncated = true
		if line.ShortRead {
			r.lineShortRead = true
		}
		return
	}
	if line.ShortRead {
		r.lineShortRead = true
	}
	if r.count < len(r.lines) {
		r.lines[r.count] = line
		r.count++
		return
	}
	r.lines[r.next] = line
	r.next = (r.next + 1) % len(r.lines)
	r.truncated = true
}

func (r *outputLineRing) snapshot() outputSnapshot {
	lines := make([]outputLine, 0, r.count)
	if r.count < len(r.lines) {
		lines = append(lines, r.lines[:r.count]...)
	} else {
		for i := 0; i < r.count; i++ {
			idx := (r.next + i) % len(r.lines)
			lines = append(lines, r.lines[idx])
		}
	}
	return outputSnapshot{Lines: lines, LinesTruncated: r.truncated, LineShortRead: r.lineShortRead}
}

func tailOutputSnapshot(snap outputSnapshot, tailLines, maxTailBytes int, dropped uint64) outputTailResult {
	if tailLines <= 0 {
		tailLines = defaultJobOutputTailLines
	}
	lines := append([]outputLine(nil), snap.Lines...)
	if snap.HasPartial {
		lines = append(lines, snap.Partial)
	}
	start := 0
	if len(lines) > tailLines {
		start = len(lines) - tailLines
	}
	selected := append([]outputLine(nil), lines[start:]...)
	text, lineShort, tailShort := linesToLimitedText(selected, maxTailBytes)
	return outputTailResult{
		Text:           text,
		TailLines:      tailLines,
		LinesTruncated: snap.LinesTruncated,
		LineShortRead:  snap.LineShortRead || lineShort,
		TailShortRead:  tailShort,
		DroppedChunks:  dropped,
		Available:      len(lines) > 0,
	}
}

func linesToLimitedText(lines []outputLine, maxBytes int) (text string, lineShort, tailShort bool) {
	for _, line := range lines {
		if line.ShortRead {
			lineShort = true
			break
		}
	}
	if len(lines) == 0 {
		return "", lineShort, false
	}
	if maxBytes <= 0 {
		return "", lineShort, true
	}
	kept := make([]string, 0, len(lines))
	used := 0
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i].Text
		lineBytes := len(line)
		extra := lineBytes
		if len(kept) > 0 {
			extra++ // newline between retained lines
		}
		if used+extra <= maxBytes {
			kept = append(kept, line)
			used += extra
			continue
		}
		tailShort = true
		if len(kept) == 0 {
			if maxBytes < lineBytes {
				line = line[lineBytes-maxBytes:]
			}
			kept = append(kept, line)
		}
		break
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, "\n"), lineShort, tailShort
}
