package shell

import "strings"

// stripTerminalEscapes removes common terminal control sequences emitted by
// PTY-backed interactive shells, including ANSI CSI and OSC sequences used by
// iTerm2/zsh prompt integration. It is intentionally conservative and leaves
// ordinary text untouched when an escape sequence is malformed.
func stripTerminalEscapes(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			continue
		}
		switch s[i+1] {
		case '[': // CSI: ESC [ ... final byte 0x40-0x7e.
			i += 2
			for i < len(s) {
				if s[i] >= 0x40 && s[i] <= 0x7e {
					break
				}
				i++
			}
		case ']': // OSC: ESC ] ... BEL or ST (ESC \\).
			i += 2
			for i < len(s) {
				if s[i] == '\a' {
					break
				}
				if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
					i++
					break
				}
				i++
			}
		case 'P', '^', '_':
			// DCS/PM/APC use the ST terminator.
			i += 2
			for i < len(s) {
				if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
					i++
					break
				}
				i++
			}
		default:
			// Two-byte escape sequence such as ESC c. Drop both bytes.
			i++
		}
	}
	return b.String()
}
