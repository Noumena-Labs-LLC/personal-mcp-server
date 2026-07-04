package shell

import "testing"

func TestStripTerminalEscapes(t *testing.T) {
	input := "\x1b]133;A\x07\x1b]1337;RemoteHost=user@host\x1b\\\x1b[?2004hok\x1b[0m\n"
	got := stripTerminalEscapes(input)
	if got != "ok\n" {
		t.Fatalf("unexpected stripped output %q", got)
	}
}

func TestStripTerminalEscapesLeavesPlainText(t *testing.T) {
	input := "hello (https://example.com/a/b)\n"
	if got := stripTerminalEscapes(input); got != input {
		t.Fatalf("plain text changed: %q", got)
	}
}
