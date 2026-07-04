package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func auditCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("audit")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("audit " + args[0]) {
			printCommandHelp("audit")
		}
		return
	}
	switch args[0] {
	case "show":
		auditShow(args[1:])
	case "tail":
		auditTail(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

type auditFilter struct {
	Tool     string
	Decision string
	Contains string
	Format   string
}

func auditShow(args []string) {
	fs := flag.NewFlagSet("audit show", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	last := fs.Int("last", 50, "number of matching audit log lines to show")
	filter := addAuditFilterFlags(fs)
	_ = fs.Parse(args)
	path := auditPathFromConfig(*configPath)
	lines, err := lastLines(path, *last, *filter)
	if err != nil {
		log.Fatal(err)
	}
	printAuditLines(lines, *filter)
}

func auditTail(args []string) {
	fs := flag.NewFlagSet("audit tail", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	last := fs.Int("last", 50, "number of matching audit log lines to show before following")
	follow := fs.Bool("follow", false, "continue printing new matching audit log lines")
	interval := fs.Duration("interval", time.Second, "poll interval when --follow is set")
	filter := addAuditFilterFlags(fs)
	_ = fs.Parse(args)
	path := auditPathFromConfig(*configPath)
	lines, err := lastLines(path, *last, *filter)
	if err != nil {
		log.Fatal(err)
	}
	printAuditLines(lines, *filter)
	if !*follow {
		fmt.Fprintf(os.Stderr, "audit tail is a snapshot; add --follow to keep watching: %s\n", path)
		return
	}
	if err := followAudit(path, *filter, *interval); err != nil {
		log.Fatal(err)
	}
}

func addAuditFilterFlags(fs *flag.FlagSet) *auditFilter {
	filter := &auditFilter{}
	fs.StringVar(&filter.Tool, "tool", "", "only show JSON audit events with this tool value")
	fs.StringVar(&filter.Decision, "decision", "", "only show JSON audit events with this decision/action value")
	fs.StringVar(&filter.Contains, "contains", "", "only show lines containing this text")
	fs.StringVar(&filter.Format, "format", "raw", "output format: raw or pretty")
	return filter
}

func auditPathFromConfig(configPath string) string {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	if strings.TrimSpace(cfg.Audit.Path) == "" {
		log.Fatal("audit.path is not set in config; use --audit-log when serving or set [audit].path")
	}
	return cfg.Audit.Path
}

func lastLines(path string, n int, filter auditFilter) ([]string, error) {
	if n <= 0 {
		n = 50
	}
	b, err := os.ReadFile(path) //nolint:gosec // audit path is local user config.
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return nil, nil
	}
	all := strings.Split(text, "\n")
	lines := make([]string, 0, min(n, len(all)))
	for _, line := range all {
		if auditLineMatches(line, filter) {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func followAudit(path string, filter auditFilter, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	var offset int64
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
	}
	for {
		time.Sleep(interval)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() < offset {
			offset = 0
		}
		if info.Size() == offset {
			continue
		}
		chunk, nextOffset, err := readAuditChunk(path, offset)
		if err != nil {
			return err
		}
		offset = nextOffset
		for _, line := range strings.Split(strings.TrimRight(chunk, "\n"), "\n") {
			if line != "" && auditLineMatches(line, filter) {
				printAuditLines([]string{line}, filter)
			}
		}
	}
}

func readAuditChunk(path string, offset int64) (chunk string, nextOffset int64, err error) {
	f, err := os.Open(path) //nolint:gosec // audit path is local user config.
	if err != nil {
		return "", offset, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", offset, err
	}
	return string(b), offset + int64(len(b)), nil
}

func auditLineMatches(line string, filter auditFilter) bool {
	if filter.Contains != "" && !strings.Contains(line, filter.Contains) {
		return false
	}
	if filter.Tool == "" && filter.Decision == "" {
		return true
	}
	fields := map[string]any{}
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return false
	}
	if filter.Tool != "" && stringField(fields, "tool") != filter.Tool {
		return false
	}
	if filter.Decision != "" && stringField(fields, "decision") != filter.Decision && stringField(fields, "action") != filter.Decision {
		return false
	}
	return true
}

func printAuditLines(lines []string, filter auditFilter) {
	for _, line := range lines {
		fmt.Println(formatAuditLine(line, filter.Format))
	}
}

func formatAuditLine(line, format string) string {
	switch format {
	case "", "raw":
		return line
	case "pretty":
		fields := map[string]any{}
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			return line
		}
		parts := []string{}
		for _, key := range []string{"ts", "tool", "decision", "action", "path", "name", "error"} {
			if value := stringField(fields, key); value != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", key, value))
			}
		}
		if len(parts) == 0 {
			return line
		}
		return strings.Join(parts, " ")
	default:
		return line
	}
}

func stringField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	s, ok := value.(string)
	if ok {
		return s
	}
	return fmt.Sprint(value)
}
