package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wheel(x, y int, down bool) tea.MouseMsg {
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: b}
}

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func TestCommitModel_MouseSidePane(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	// rows: 0 Staged header, 1 both.go, 2 staged.go, 3 Unstaged header, 4 a.go, 5 both.go, 6 new.txt
	cmd := c.Update(wheel(3, 5, true))
	runCmd(t, c, cmd)
	assert.Equal(t, 2, c.list.CursorIndex())
	assert.Equal(t, "staged.go", c.staging.spec.Path)
	c.Update(wheel(3, 5, false))
	assert.Equal(t, 1, c.list.CursorIndex())

	// click row 4 (a.go): y = 3 (border + tabs + spacer) + 4
	c.focus = focusDiff
	cmd = c.Update(click(3, 7))
	runCmd(t, c, cmd)
	assert.Equal(t, focusSide, c.focus)
	fr, ok := c.cursorFile()
	require.True(t, ok)
	assert.Equal(t, "a.go", fr.file.Path)
	assert.False(t, fr.staged)

	// clicks on headers, borders and empty rows do nothing
	before := c.list.CursorIndex()
	assert.Nil(t, c.Update(click(3, 3)))
	assert.Nil(t, c.Update(click(3, 0)))
	assert.Nil(t, c.Update(click(3, 2)))
	assert.Nil(t, c.Update(click(3, 30)))
	assert.Equal(t, before, c.list.CursorIndex())
	assert.Nil(t, c.Update(tea.MouseMsg{X: 3, Y: 7, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}))
}

func TestCommitModel_MouseDiffPane(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	x := c.sideWidth() + 5
	// wheel over the diff column reaches the embedded model's debounced scroll
	cmd := c.Update(wheel(x, 10, true))
	assert.True(t, c.diff.wheel.renderPending || cmd == nil, "wheel is handled by the diff model")
	// a click in the diff places the cursor and focuses the pane
	c.Update(click(x, 3))
	assert.Equal(t, focusDiff, c.focus)
	// the debounce message is forwarded to the diff model
	c.Update(wheelDebounceMsg{gen: c.diff.wheel.gen})
	assert.False(t, c.diff.wheel.renderPending)

	// overlays swallow mouse events. an unsized model ignores them
	c.Update(keyRunes("?"))
	assert.Nil(t, c.Update(click(x, 3)))
	assert.True(t, c.overlay.Active())
	fresh, err := NewCommitModel(*testCommitConfig())
	require.NoError(t, err)
	assert.Nil(t, fresh.Update(click(1, 1)))
}

func TestCommitModel_GlobalNavigation(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	c.installRaw(c.staging.spec, twoBlockRaw)
	// ] and [ work from the side pane
	assert.Equal(t, focusSide, c.focus)
	assert.Equal(t, 1, c.diff.paneCursor())
	press(t, c, "]")
	assert.Equal(t, 2, c.diff.paneCursor(), "first change block")
	press(t, c, "]")
	assert.Equal(t, 5, c.diff.paneCursor(), "second change block")
	press(t, c, "[")
	assert.Equal(t, 2, c.diff.paneCursor())
	// n / p move the file list even while the diff pane has focus
	press(t, c, "enter")
	assert.Equal(t, focusDiff, c.focus)
	press(t, c, "n")
	fr, _ := c.cursorFile()
	assert.Equal(t, "staged.go", fr.file.Path)
	press(t, c, "p")
	fr, _ = c.cursorFile()
	assert.Equal(t, "both.go", fr.file.Path)
	press(t, c, "N")
	fr, _ = c.cursorFile()
	assert.Equal(t, "both.go", fr.file.Path, "already at the first item")
	// on a history tab n/p walk that list
	press(t, c, "2")
	press(t, c, "n")
	b, ok := c.cursorBranch()
	require.True(t, ok)
	assert.Equal(t, "feature", b.Name)
	// empty diff: hunk keys are inert
	c.installRaw(c.staging.spec, "")
	assert.Nil(t, c.Update(keyRunes("]")))
}
