package fsx

import (
	"fmt"
	"strings"
)

func compactUnifiedDiff(path, oldText, newText string, contextLines int, maxBytes int64) (string, bool) {
	if oldText == newText {
		return "", false
	}
	if contextLines < 0 {
		contextLines = 3
	}
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	return formatSingleRegionUnifiedDiff(path, oldLines, newLines, contextLines, maxBytes)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.SplitAfter(s, "\n")
}

func formatSingleRegionUnifiedDiff(path string, oldLines, newLines []string, contextLines int, maxBytes int64) (string, bool) {
	prefix := commonPrefixLines(oldLines, newLines)
	suffix := commonSuffixLines(oldLines[prefix:], newLines[prefix:])
	oldChangeStart := prefix
	newChangeStart := prefix
	oldChangeEnd := len(oldLines) - suffix
	newChangeEnd := len(newLines) - suffix
	oldHunkStart := maxInt(0, oldChangeStart-contextLines)
	newHunkStart := maxInt(0, newChangeStart-contextLines)
	oldHunkEnd := minInt(len(oldLines), oldChangeEnd+contextLines)
	newHunkEnd := minInt(len(newLines), newChangeEnd+contextLines)

	var b strings.Builder
	b.WriteString("--- " + path + "\n")
	b.WriteString("+++ " + path + "\n")
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldHunkStart+1, oldHunkEnd-oldHunkStart, newHunkStart+1, newHunkEnd-newHunkStart)
	for _, line := range oldLines[oldHunkStart:oldChangeStart] {
		b.WriteByte(' ')
		b.WriteString(ensureNL(line))
	}
	for _, line := range oldLines[oldChangeStart:oldChangeEnd] {
		b.WriteByte('-')
		b.WriteString(ensureNL(line))
		if maxBytes > 0 && int64(b.Len()) > maxBytes {
			return truncateDiff(b.String(), maxBytes), true
		}
	}
	for _, line := range newLines[newChangeStart:newChangeEnd] {
		b.WriteByte('+')
		b.WriteString(ensureNL(line))
		if maxBytes > 0 && int64(b.Len()) > maxBytes {
			return truncateDiff(b.String(), maxBytes), true
		}
	}
	for _, line := range oldLines[oldChangeEnd:oldHunkEnd] {
		b.WriteByte(' ')
		b.WriteString(ensureNL(line))
	}
	out := b.String()
	if maxBytes > 0 && int64(len(out)) > maxBytes {
		return truncateDiff(out, maxBytes), true
	}
	return out, false
}

func commonPrefixLines(a, b []string) int {
	limit := minInt(len(a), len(b))
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

func commonSuffixLines(a, b []string) int {
	limit := minInt(len(a), len(b))
	for i := 0; i < limit; i++ {
		if a[len(a)-1-i] != b[len(b)-1-i] {
			return i
		}
	}
	return limit
}

func truncateDiff(s string, maxBytes int64) string {
	if maxBytes <= 0 || int64(len(s)) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n... diff truncated ...\n"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ensureNL(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
