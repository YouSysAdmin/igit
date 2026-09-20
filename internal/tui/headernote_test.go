package tui

import (
	"strings"
	"testing"

	"github.com/yousysadmin/igit/internal/tui/sidepane"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
)

func TestModel_HeaderNote(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{NoTree: true})
	m.filesLoaded = true
	next, _ := m.handleResize(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	next, _ = m.handleFileLoaded(fileLoadedMsg{file: "f.go", seq: m.file.loadSeq, lines: stageLines()})
	m = next.(Model)
	base := m.layout.viewport.Height
	assert.Equal(t, 0, m.noteRows())
	assert.Equal(t, 2, m.diffTopRow())

	m.setHeaderNote("feat: subject\n\nbody paragraph one\nbody line two\n")
	lines := m.headerNoteLines()
	assert.Equal(t, []string{"", "  feat: subject", "  ", "  body paragraph one", "  body line two", ""}, lines)
	assert.Equal(t, base-len(lines), m.layout.viewport.Height, "the viewport gives up the note rows")
	assert.Equal(t, 2+len(lines), m.diffTopRow())
	view := m.View()
	assert.Contains(t, view, "feat: subject")
	assert.Contains(t, view, "body line two")
	rows := strings.Split(view, "\n")
	assert.Contains(t, rows[1], "f.go", "header stays first")
	assert.Contains(t, rows[3], "feat: subject", "note follows a blank spacer row")

	// resize keeps the note accounted for
	next, _ = m.handleResize(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(Model)
	assert.Equal(t, m.paneHeight()-1-len(lines), m.layout.viewport.Height)

	// long notes are capped with an ellipsis row
	m.setHeaderNote(strings.Repeat("line\n", 40))
	capped := m.headerNoteLines()
	assert.Len(t, capped, headerNoteMaxRows)
	assert.Equal(t, "  …", capped[len(capped)-2])

	// clearing restores the full viewport
	m.setHeaderNote("")
	assert.Equal(t, 0, m.noteRows())
	assert.Equal(t, m.paneHeight()-1, m.layout.viewport.Height)
	assert.Empty(t, m.renderHeaderNote(70))
}

func TestModel_HeaderNote_beforeReady(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	m.setHeaderNote("x")
	assert.Equal(t, "x", m.headerNote)
	assert.False(t, m.ready)
}

func TestWrapWords(t *testing.T) {
	assert.Equal(t, []string{"aaa bbb", "ccc"}, wrapWords("aaa bbb ccc", 8))
	assert.Equal(t, []string{"averyveryverylongword", "x"}, wrapWords("averyveryverylongword x", 5))
	assert.Equal(t, []string{""}, wrapWords("   ", 10))
	_ = git.ChangeAdd
	require.Len(t, wrapWords("a b c d", 3), 2)
}

func TestTruncateTail(t *testing.T) {
	assert.Equal(t, "abc", sidepane.TruncateRight("abc", 5))
	assert.Equal(t, "ab…", sidepane.TruncateRight("abcdef", 3))
	assert.Equal(t, "…", sidepane.TruncateRight("abcdef", 1))
}
