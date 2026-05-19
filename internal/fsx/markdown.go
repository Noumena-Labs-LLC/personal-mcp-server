package fsx

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type MarkdownSection struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Level     int    `json:"level"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
}

func ParseMarkdownSections(content string) []MarkdownSection {
	return markdownSections(content)
}

func FindMarkdownSection(sections []MarkdownSection, selector string) (MarkdownSection, error) {
	return findMarkdownSection(sections, selector)
}

func MarkdownSectionContent(content string, section MarkdownSection) string {
	return joinLines(splitLinesKeepEnd(content), section.LineStart, section.LineEnd)
}

type markdownHeading struct {
	Title string
	Level int
	Line  int
}

var atxHeadingRE = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$`)

func markdownSections(content string) []MarkdownSection {
	lines := splitLinesKeepEnd(content)
	headings := markdownHeadings(lines)
	sections := make([]MarkdownSection, 0, len(headings))
	seenIDs := map[string]int{}
	for i, h := range headings {
		end := len(lines)
		for j := i + 1; j < len(headings); j++ {
			if headings[j].Level <= h.Level {
				end = headings[j].Line - 1
				break
			}
		}
		sections = append(sections, MarkdownSection{ID: uniqueSlug(h.Title, seenIDs), Title: h.Title, Level: h.Level, LineStart: h.Line, LineEnd: end})
	}
	return sections
}

func markdownHeadings(lines []string) []markdownHeading {
	headings := []markdownHeading{}
	inFence := false
	fenceMarker := ""
	for i, line := range lines {
		trimmedLeft := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmedLeft, "```") || strings.HasPrefix(trimmedLeft, "~~~") {
			marker := trimmedLeft[:3]
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if inFence {
			continue
		}
		m := atxHeadingRE.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if m == nil {
			continue
		}
		title := strings.TrimSpace(m[2])
		if title == "" {
			continue
		}
		headings = append(headings, markdownHeading{Title: title, Level: len(m[1]), Line: i + 1})
	}
	return headings
}

func findMarkdownSection(sections []MarkdownSection, selector string) (MarkdownSection, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return MarkdownSection{}, errors.New("section is required")
	}
	selectorSlug := slugify(selector)
	var matches []MarkdownSection
	for _, section := range sections {
		if section.ID == selector || section.ID == selectorSlug || strings.EqualFold(section.Title, selector) {
			matches = append(matches, section)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return MarkdownSection{}, fmt.Errorf("section %q is ambiguous; use a unique section id", selector)
	}
	return MarkdownSection{}, fmt.Errorf("section %q not found", selector)
}

func uniqueSlug(title string, seen map[string]int) string {
	base := slugify(title)
	if base == "" {
		base = "section"
	}
	seen[base]++
	if seen[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, seen[base])
}

func slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || unicode.IsSpace(r) || r == '_' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func splitLinesKeepEnd(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.SplitAfter(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func joinLines(lines []string, startLine, endLine int) string {
	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine || startLine > len(lines) {
		return ""
	}
	return strings.Join(lines[startLine-1:endLine], "")
}

func markdownHeadingLine(level int, title string) (string, error) {
	if level <= 0 {
		level = 2
	}
	if level > 6 {
		return "", errors.New("heading level must be between 1 and 6")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("title is required")
	}
	return strings.Repeat("#", level) + " " + title + "\n", nil
}

func normalizeMarkdownBlock(s string) string {
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}
