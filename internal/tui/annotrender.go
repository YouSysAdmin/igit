package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui/style"
)

// The rows review mode draws on top of the diff: its own annotations, the live
// annotation editor and the pull-request comments loaded for the file. The
// pane asks for them through decorations, so a host with nothing to attach
// (commit mode) renders the diff alone.

// renderFileAnnotationHeader writes the file-level annotation or input to the builder.
func (m Model) renderFileAnnotationHeader(b *strings.Builder, dec decorations) {
	// when actively editing a file-level annotation, always show the input widget
	if dec.editor.active && dec.editor.onFile {
		m.renderAnnotationInput(b, m.annotFilePrefix())
		return
	}

	// gate on hasFile, not on a non-empty body: an empty-body file-level
	// annotation loaded via --annotations still reserves a viewport row via
	// hasFileAnnotation+wrappedAnnotationLineCount, and the chokepoint emits a
	// single prefix-only row when body is empty. gating on the body would skip
	// paint while the height query still reserves the row.
	if dec.hasFile {
		cursor := " "
		if m.nav.diffCursor == -1 && m.layout.focus == paneDiff {
			cursor = m.renderer.DiffCursor(m.cfg.noColors)
		}
		m.renderWrappedAnnotation(b, cursor, m.annotFilePrefix(), dec.fileComment)
	}
}

// renderAnnotationOrInput writes the annotation input or existing annotation below a diff line.
func (m Model) renderAnnotationOrInput(b *strings.Builder, idx int, dec decorations) {
	if dec.editsRow(idx, m.nav.diffCursor) {
		m.renderAnnotationInput(b, m.annotPrefix())
		return
	}
	dl := m.file.lines[idx]
	if dl.ChangeType == git.ChangeDivider {
		return
	}
	comment, hasLocal, remotes := m.decorationsFor(dec, dl)
	cursor := " "
	if idx == m.nav.diffCursor && dec.editor.onCursor && m.layout.focus == paneDiff {
		cursor = m.renderer.DiffCursor(m.cfg.noColors)
	}
	for _, seg := range m.annotationSegments(hasLocal, m.annotPrefix(), comment, remotes) {
		m.renderWrappedAnnotation(b, cursor, seg.prefix, seg.body)
		cursor = " " // only the first row of the block carries the cursor
	}
}

// renderAnnotationInput writes the annotation editor rows. The prefix leads the
// first row and continuation rows keep an indent of the same width, so the live
// editor lays out like the saved annotation will.
func (m Model) renderAnnotationInput(b *strings.Builder, prefix string) {
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	paneBg := m.resolver.Color(style.ColorKeyDiffPaneBg)
	for i, row := range strings.Split(m.annot.input.View(), "\n") {
		lead := indent
		if i == 0 {
			lead = m.renderer.AnnotationInline(prefix)
		}
		// strip the editor's unstyled trailing padding so extendLineBg can re-pad with DiffBg
		line := strings.TrimRight(" "+lead+row, " ")
		b.WriteString(m.extendLineBg(line, paneBg) + "\n")
	}
}

// renderWrappedAnnotation writes an annotation line with word wrapping.
// annotations always wrap regardless of wrapMode since they contain prose.
// delegates all wrap+style logic to annotationVisualRows (the chokepoint). this
// painter only prepends the cursor cell on row 0 and pads each visual row with
// DiffPaneBg via extendLineBg so themed pane backgrounds extend across the full
// width rather than falling back to terminal default on the right portion.
func (m Model) renderWrappedAnnotation(b *strings.Builder, cursor, prefix, body string) {
	rows := m.annotationVisualRows(prefix, body)
	if len(rows) == 0 {
		return
	}
	paneBg := m.resolver.Color(style.ColorKeyDiffPaneBg)
	for i, row := range rows {
		c := " "
		if i == 0 {
			c = cursor
		}
		b.WriteString(m.extendLineBg(c+row, paneBg) + "\n")
	}
}
