package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pickItems() []PickItem {
	return []PickItem{
		{ID: "12", Label: "#12  Fix parser", Detail: "alice · fix-parser"},
		{ID: "11", Label: "#11  Add docs", Detail: "bob · docs"},
		{ID: "10", Label: "#10  Refactor", Detail: "alice · refactor"},
	}
}

func step(t *testing.T, p PickList, msg tea.Msg) (PickList, tea.Cmd) {
	t.Helper()
	next, cmd := p.Update(msg)
	got, ok := next.(PickList)
	require.True(t, ok)
	return got, cmd
}

func TestPickList_NavigateAndPick(t *testing.T) {
	p := NewPickList("Open pull requests", pickItems())
	p, _ = step(t, p, tea.WindowSizeMsg{Width: 80, Height: 20})
	view := p.View()
	assert.Contains(t, view, "Open pull requests")
	assert.Contains(t, view, "> #12  Fix parser")
	assert.Contains(t, view, "3 of 3")

	p, _ = step(t, p, tea.KeyMsg{Type: tea.KeyDown})
	assert.Contains(t, p.View(), "> #11  Add docs")
	p, _ = step(t, p, tea.KeyMsg{Type: tea.KeyCtrlP})
	p, _ = step(t, p, tea.KeyMsg{Type: tea.KeyUp})
	assert.Contains(t, p.View(), "> #10  Refactor", "moving wraps around")

	p, cmd := step(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	assert.Equal(t, "10", p.Chosen())
	assert.False(t, p.Canceled())
}

func TestPickList_FilterAndCancel(t *testing.T) {
	p := NewPickList("t", pickItems())
	for _, r := range "alice" {
		p, _ = step(t, p, keyRunes(string(r)))
	}
	assert.Contains(t, p.View(), "2 of 3")
	assert.NotContains(t, p.View(), "Add docs")
	p, _ = step(t, p, tea.KeyMsg{Type: tea.KeyDown})
	p, _ = step(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "10", p.Chosen(), "the filter narrows what the cursor walks")

	p = NewPickList("t", pickItems())
	for _, r := range "zzz" {
		p, _ = step(t, p, keyRunes(string(r)))
	}
	assert.Contains(t, p.View(), "no matches")
	p, cmd := step(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	assert.Nil(t, cmd, "enter with no match does nothing")
	p, cmd = step(t, p, tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, cmd)
	assert.True(t, p.Canceled())
	assert.Empty(t, p.Chosen())
}

func TestPickList_EmptyAndNarrow(t *testing.T) {
	p := NewPickList("t", nil)
	p, _ = step(t, p, tea.WindowSizeMsg{Width: 20, Height: 6})
	view := p.View()
	assert.Contains(t, view, "no matches")
	p, cmd := step(t, p, tea.KeyMsg{Type: tea.KeyDown})
	assert.Nil(t, cmd)
	assert.Equal(t, 0, p.cursor)
	assert.NotNil(t, p.Init())
}
