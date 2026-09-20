package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/tui/style"
)

// headerNoteMaxRows bounds the note block so a long commit body cannot push
// the diff off screen.
const headerNoteMaxRows = 12

// setHeaderNote shows text (a commit message, say) between the diff header and
// the diff rows. Empty text removes the block. The viewport shrinks by the
// block's height so the pane keeps its size.
func (m *Model) setHeaderNote(text string) {
	m.headerNote = strings.TrimSpace(text)
	if !m.ready {
		return
	}
	m.layout.viewport.Height = max(1, m.paneHeight()-1-m.noteRows())
	if len(m.file.lines) > 0 {
		m.syncViewportToCursor()
	}
}

// noteRows is the number of rows the header note occupies (0 when unset).
func (m Model) noteRows() int { return len(m.headerNoteLines()) }

// diffPaneInnerWidth is the width inside the diff pane borders.
func (m Model) diffPaneInnerWidth() int {
	if m.treePaneHidden() {
		return m.layout.width - 2
	}
	return m.layout.width - m.layout.treeWidth - 4
}

// headerNoteLines wraps the note to the pane width: a blank row, the
// paragraphs indented by two cells, a blank row. Overflow past
// headerNoteMaxRows is replaced by an ellipsis row.
func (m Model) headerNoteLines() []string {
	if m.headerNote == "" {
		return nil
	}
	width := max(m.diffPaneInnerWidth()-3, 8)
	lines := []string{""}
	for para := range strings.SplitSeq(m.headerNote, "\n") {
		for _, l := range wrapWords(strings.TrimRight(para, " \t"), width) {
			lines = append(lines, "  "+l)
		}
	}
	if len(lines) > headerNoteMaxRows-1 {
		lines = append(lines[:headerNoteMaxRows-2], "  …")
	}
	return append(lines, "")
}

// renderHeaderNote styles the note rows: the first text row (the subject)
// in the accent style, the rest as plain entries.
func (m Model) renderHeaderNote(width int) []string {
	lines := m.headerNoteLines()
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	subjectDone := false
	for _, l := range lines {
		switch {
		case l == "":
			out = append(out, "")
		case !subjectDone:
			subjectDone = true
			out = append(out, m.resolver.Style(style.StyleKeyDirEntry).MaxWidth(width).Render(l))
		default:
			out = append(out, m.resolver.Style(style.StyleKeyFileEntry).MaxWidth(width).Render(l))
		}
	}
	return out
}

// wrapWords breaks text into lines of at most width cells at word boundaries.
// a single word longer than width stays on its own line.
func wrapWords(text string, width int) []string {
	if strings.TrimSpace(text) == "" {
		return []string{""}
	}
	var out []string
	line := ""
	for word := range strings.FieldsSeq(text) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	return append(out, line)
}
