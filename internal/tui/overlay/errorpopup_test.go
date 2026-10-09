package overlay

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlainOutput(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"colors stripped", "\x1b[31mFAIL\x1b[0m lint", "FAIL lint"},
		{"crlf collapsed", "a\r\nb\r\n", "a\nb"},
		{"progress keeps the last state", "Writing 1/3\rWriting 2/3\rWriting 3/3\ndone", "Writing 3/3\ndone"},
		{"indentation kept", "  File \"x.py\", line 1\n    raise E", "  File \"x.py\", line 1\n    raise E"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, plainOutput(tc.in))
		})
	}
}

func TestErrorOverlay_renderKeepsIndentationAndWraps(t *testing.T) {
	m := NewManager()
	long := strings.Repeat("x", 150)
	m.OpenError(ErrorSpec{Title: "commit failed", Summary: "exit status 1", Detail: "    indented line\n" + long})
	out := m.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), popupCtx())
	require.Contains(t, out, "commit failed")
	assert.Contains(t, out, "    indented line")
	assert.NotContains(t, out, long, "a long line is wrapped to the popup width")
	assert.Contains(t, out, strings.Repeat("x", 40))
}
