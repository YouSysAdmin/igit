package tui

import (
	"strings"

	"github.com/yousysadmin/igit/internal/tui/sidepane"
)

// Scrollbar glyphs: the track is the lipgloss right border, the thumb is a
// heavy vertical of the same width. The thumb is wrapped in bold and closed
// with an intensity-only reset so the border colors around it survive. Bold
// is emitted in --no-colors mode too, as it is a weight, not a color.
const (
	scrollbarTrackRune = "│"
	scrollbarThumbRune = "\x1b[1m┃\x1b[22m"

	// the rendered navigation pane has 1 top border row before content rows begin.
	// if the tree pane gets a header or any other pre-content row, this offset
	// must be updated in lockstep.
	navigationScrollbarFirstViewportRow = 1

	// the rendered diff pane has 1 top border row + 1 header row before the
	// viewport rows begin. the single-line header invariant is enforced by
	// truncateHeaderTitle in view.go - if the diff pane's pre-viewport row
	// count ever changes (multi-line header, status pill above viewport,
	// etc.), this offset must be updated in lockstep.
	diffScrollbarFirstViewportRow = 2
)

// scrollbarSpec describes a scrollable pane's viewport for thumb placement.
// callers (applyScrollbar / applyNavigationScrollbar) populate it from their
// respective scroll-state sources. applyPaneScrollbar is otherwise pane-agnostic.
type scrollbarSpec struct {
	total            int // total content rows in the scrollable region
	height           int // visible row count (vh) inside the pane
	offset           int // first visible row index (0-based) into the content
	firstViewportRow int // line index in the rendered pane where the viewport's first row sits
}

// applyScrollbar replaces the right-border rune of diff viewport rows with a
// thicker thumb glyph (heavy-vertical, bold) to indicate scroll position.
// see applyPaneScrollbar for no-op cases, layout-shape invariants, and ANSI
// envelope handling.
func (m Model) applyScrollbar(rendered string) string {
	return m.applyPaneScrollbar(rendered, scrollbarSpec{
		total:            m.layout.viewport.TotalLineCount(),
		height:           m.layout.viewport.Height,
		offset:           m.layout.viewport.YOffset,
		firstViewportRow: diffScrollbarFirstViewportRow + m.noteRows(),
	})
}

// applyNavigationScrollbar replaces the right-border rune of tree rows with
// the same thumb used by the diff pane. state must come from the component's
// ScrollState() AFTER Render(), so Offset reflects the latest cursor/scroll
// position. passing a stale ScrollState would silently misposition the thumb.
// see applyPaneScrollbar for no-op cases and ANSI envelope handling.
func (m Model) applyNavigationScrollbar(rendered string, state sidepane.ScrollState) string {
	return m.applyPaneScrollbar(rendered, scrollbarSpec{
		total:            state.Total,
		height:           m.paneHeight(),
		offset:           state.Offset,
		firstViewportRow: navigationScrollbarFirstViewportRow,
	})
}

// applyPaneScrollbar swaps the right-border rune of the rows covered by the
// visible portion for the thumb glyph. Only that rune is replaced, so the
// border ANSI envelope survives and the geometry is unchanged. It is a no-op
// when the content fits, when the viewport is empty and when the rendered
// pane does not have the expected row count (a soft-wrap would misplace the
// thumb).
func (m Model) applyPaneScrollbar(rendered string, spec scrollbarSpec) string {
	total := spec.total
	vh := spec.height
	if total <= vh || vh <= 0 {
		return rendered
	}

	lines := strings.Split(rendered, "\n")
	// shape check: rendered pane must have at least firstViewportRow+vh+1 rows
	// (pre-content rows + vh viewport rows + bottom) and at most paneHeight()+2
	// rows (lipgloss outer height with padding). more than the upper bound means
	// content soft-wrapped somewhere and the thumb would land on non-viewport rows.
	// bail. less than the lower bound means the caller passed a truncated render.
	// bail. tests using synthetic input hit the minimum bound. production hits the
	// maximum (vh = paneHeight()-1 for diff, vh = paneHeight() for navigation).
	minRows := spec.firstViewportRow + vh + 1
	maxRows := max(minRows, m.paneHeight()+2)
	if len(lines) < minRows || len(lines) > maxRows {
		return rendered
	}

	thumbSize := min(vh, max(1, vh*vh/total))

	// the divisor below (total - vh) is guaranteed > 0 by the early-return above.
	// no zero-divisor guard needed. when vh == 1, thumbSize == 1 and maxStart == 0,
	// so thumbStart resolves to 0 regardless of offset and the single thumb row
	// anchors at the only viewport row.
	maxStart := vh - thumbSize
	thumbStart := min(maxStart, spec.offset*maxStart/(total-vh))

	for i := range vh {
		if i < thumbStart || i >= thumbStart+thumbSize {
			continue
		}
		rowIdx := spec.firstViewportRow + i
		if rowIdx >= len(lines) {
			break
		}
		before, after, ok := strings.CutLast(lines[rowIdx], scrollbarTrackRune)
		if !ok {
			continue
		}
		lines[rowIdx] = before + scrollbarThumbRune + after
	}
	return strings.Join(lines, "\n")
}
