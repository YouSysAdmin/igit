package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yousysadmin/igit/internal/git"
)

func TestPaneHost_zeroInReviewMode(t *testing.T) {
	m := testModel(nil, nil)
	assert.False(t, m.host.embedded, "a review model owns its whole window")
	assert.False(t, m.host.unfocused)
	assert.False(t, m.host.annotationsHidden)
	assert.False(t, m.hostSelected(0), "no host predicate means no row is selected")
}

func TestPaneHost_setters(t *testing.T) {
	m := testModel(nil, nil)

	m.embedAsPane()
	assert.True(t, m.host.embedded)

	m.setPaneUnfocused(true)
	assert.True(t, m.host.unfocused)

	m.setPaneAnnotationsHidden(true)
	assert.True(t, m.host.annotationsHidden)

	m.setPaneLineSelected(func(idx int) bool { return idx == 2 })
	assert.False(t, m.hostSelected(1))
	assert.True(t, m.hostSelected(2))

	m.setPaneLineSelected(nil)
	assert.False(t, m.hostSelected(2), "clearing the predicate deselects every row")
}

func TestPaneToggleOn(t *testing.T) {
	m := testModel(nil, nil)
	for _, tg := range []paneToggle{toggleWrap, toggleLineNumbers, toggleWordDiff, toggleBlame, toggleCompact, toggleCollapsed} {
		assert.False(t, m.paneToggleOn(tg), "a fresh model has every view toggle off")
	}

	m.modes.wrap = true
	m.modes.lineNumbers = true
	m.modes.wordDiff = true
	m.modes.showBlame = true
	m.modes.compact = true
	m.modes.collapsed.enabled = true
	for _, tg := range []paneToggle{toggleWrap, toggleLineNumbers, toggleWordDiff, toggleBlame, toggleCompact, toggleCollapsed} {
		assert.True(t, m.paneToggleOn(tg))
	}
}

func TestSetPaneCursor_clampsAndIgnoresAnEmptyPane(t *testing.T) {
	m := testModel(nil, nil)
	m.setPaneCursor(5)
	assert.Equal(t, 0, m.paneCursor(), "an empty pane has nowhere to put the cursor")

	m.file.lines = []git.DiffLine{
		{NewNum: 1, Content: "a", ChangeType: git.ChangeContext},
		{NewNum: 2, Content: "b", ChangeType: git.ChangeContext},
	}
	assert.Equal(t, 2, m.paneRowCount())

	m.setPaneCursor(99)
	assert.Equal(t, 1, m.paneCursor(), "past the end lands on the last row")
	m.setPaneCursor(-3)
	assert.Equal(t, 0, m.paneCursor(), "before the start lands on the first row")
}

func TestNextPaneLoad_isMonotonic(t *testing.T) {
	m := testModel(nil, nil)
	first := m.nextPaneLoad()
	assert.Equal(t, first+1, m.nextPaneLoad())
}
