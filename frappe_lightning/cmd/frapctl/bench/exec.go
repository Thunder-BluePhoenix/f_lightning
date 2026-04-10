package bench

import (
	"os/exec"
	"strings"
)

// newCmd is a thin wrapper so tests can swap the exec backend.
func newCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// splitLines splits s on \n and \r\n and trims each element.
func splitLines(s string) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}
