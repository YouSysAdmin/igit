package overlay

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptOverlay_submitAndCancel(t *testing.T) {
	mgr := NewManager()
	mgr.OpenPrompt(PromptSpec{ID: "commit", Title: "commit message", Placeholder: "summary", Editor: "edit in $EDITOR"})
	assert.Equal(t, KindPrompt, mgr.Kind())
	assert.Empty(t, mgr.PromptValue())

	// empty submit is ignored
	assert.Equal(t, OutcomeNone, mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}, "").Kind)
	assert.True(t, mgr.Active())

	mgr.HandleKey(runes("fix bug"), "")
	assert.Equal(t, "fix bug", mgr.PromptValue())
	view := mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), popupCtx())
	assert.Contains(t, view, "commit message")
	assert.Contains(t, view, "fix bug")
	assert.Contains(t, view, "[ctrl+o] edit in $EDITOR")
	assert.NotContains(t, view, "history")

	out := mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}, "")
	assert.Equal(t, OutcomePromptSubmitted, out.Kind)
	assert.Equal(t, "commit", out.PromptID)
	assert.Equal(t, "fix bug", out.PromptValue)
	assert.False(t, mgr.Active())
	assert.Empty(t, mgr.PromptValue(), "closed prompt has no value")

	mgr.OpenPrompt(PromptSpec{ID: "x", Initial: "draft"})
	assert.Equal(t, "draft", mgr.PromptValue())
	assert.Equal(t, OutcomeClosed, mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEsc}, "").Kind)
	assert.Contains(t, mgr.Compose("", popupCtx()), "", "closed prompt renders nothing extra")
}

func TestPromptOverlay_editorHandoff(t *testing.T) {
	mgr := NewManager()
	mgr.OpenPrompt(PromptSpec{ID: "commit"})
	mgr.HandleKey(runes("wip"), "")
	assert.Equal(t, OutcomeNone, mgr.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlO}, "").Kind, "no editor label: ctrl+o is inert")
	assert.True(t, mgr.Active())

	mgr.OpenPrompt(PromptSpec{ID: "commit", Editor: "editor"})
	mgr.HandleKey(runes("wip"), "")
	out := mgr.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlO}, "")
	assert.Equal(t, OutcomePromptEditor, out.Kind)
	assert.Equal(t, "wip", out.PromptValue)
	assert.False(t, mgr.Active())
}

func TestPromptOverlay_history(t *testing.T) {
	mgr := NewManager()
	mgr.OpenPrompt(PromptSpec{ID: "commit", History: []string{"newest\n\nbody", "older"}})
	mgr.HandleKey(runes("typed"), "")
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyUp}, "")
	assert.Equal(t, "newest", mgr.PromptValue(), "first line of the newest entry")
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlP}, "")
	assert.Equal(t, "older", mgr.PromptValue())
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyUp}, "")
	assert.Equal(t, "older", mgr.PromptValue(), "clamps at the oldest")
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyDown}, "")
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlN}, "")
	assert.Equal(t, "typed", mgr.PromptValue(), "leaving history restores the draft")
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyDown}, "")
	assert.Equal(t, "typed", mgr.PromptValue())
	assert.Contains(t, mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), popupCtx()), "[↑/↓] history")

	empty := NewManager()
	empty.OpenPrompt(PromptSpec{ID: "x"})
	empty.HandleKey(tea.KeyMsg{Type: tea.KeyUp}, "")
	assert.Empty(t, empty.PromptValue())
	assert.Equal(t, OutcomeNone, empty.HandleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}).Kind)
	require.True(t, empty.Active())
}

func TestPromptOverlay_allowEmpty(t *testing.T) {
	mgr := NewManager()
	mgr.OpenPrompt(PromptSpec{ID: "stash", AllowEmpty: true})
	out := mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}, "")
	assert.Equal(t, OutcomePromptSubmitted, out.Kind)
	assert.Empty(t, out.PromptValue)
}

func TestPromptOverlay_defaultTitle(t *testing.T) {
	mgr := NewManager()
	mgr.OpenPrompt(PromptSpec{ID: "x"})
	assert.Contains(t, mgr.Compose(strings.Repeat(strings.Repeat(" ", 100)+"\n", 29), popupCtx()), "input")
}
