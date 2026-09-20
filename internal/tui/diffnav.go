package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/keymap"
)

const (
	scrollStep = 4 // horizontal scroll step in characters

	// changePrefixWidth is the display width of the add/remove/context marker
	// linePrefix re-adds at render time (" + ", " - ", "   "). dividerPrefixWidth
	// is the single leading space renderDiffLine gives a divider row instead.
	changePrefixWidth  = 3
	dividerPrefixWidth = 1
)

// cursorDiffLine returns the DiffLine at the current cursor position, if valid.
func (m Model) cursorDiffLine() (git.DiffLine, bool) {
	if m.nav.diffCursor < 0 || m.nav.diffCursor >= len(m.file.lines) {
		return git.DiffLine{}, false
	}
	return m.file.lines[m.nav.diffCursor], true
}

// moveDiffCursorDown moves the diff cursor to the next non-divider line.
// if the current line has an annotation and cursor is on the diff line, stops on the annotation first.
// in collapsed mode, also skips removed lines unless their hunk is expanded.
func (m *Model) moveDiffCursorDown() {
	m.moveDiffCursorDownWithHunks(m.findHunks())
}

// moveDiffCursorDownWithHunks is the hunks-precomputed variant of moveDiffCursorDown.
// Callers that move the cursor repeatedly (e.g. repeatDiffAction for N j/k) call
// findHunks once and pass the result in to avoid O(N * len(diff)) rescans.
func (m *Model) moveDiffCursorDownWithHunks(hunks []int) {
	// if currently on annotation sub-line, move to the next diff line. the flag clears only once a
	// next line is found: with no line left the cursor has nowhere to go, so staying on the
	// annotation is what "no movement" means. clearing it unconditionally would walk the cursor
	// back up onto the diff row and, in moveDiffCursorDownBy, read as progress forever.
	if m.nav.onAnnotationRow {
		for i := m.nav.diffCursor + 1; i < len(m.file.lines); i++ {
			if m.file.lines[i].ChangeType != git.ChangeDivider && !m.isCollapsedHidden(i, hunks) {
				m.nav.onAnnotationRow = false
				m.nav.diffCursor = i
				return
			}
		}
		return
	}

	// if current line has an annotation, stop on it first.
	// skip for delete-only placeholders - their annotations are only visible when expanded.
	if m.nav.diffCursor >= 0 && m.nav.diffCursor < len(m.file.lines) {
		dl := m.file.lines[m.nav.diffCursor]
		if dl.ChangeType != git.ChangeDivider && !m.isDeleteOnlyPlaceholder(m.nav.diffCursor, hunks) {
			if m.hasAnnotation(dl) {
				m.nav.onAnnotationRow = true
				return
			}
		}
	}

	// move to next non-divider diff line, skipping collapsed hidden lines
	start := m.nav.diffCursor + 1
	if m.nav.diffCursor == -1 {
		start = 0
	}
	for i := start; i < len(m.file.lines); i++ {
		if m.file.lines[i].ChangeType != git.ChangeDivider && !m.isCollapsedHidden(i, hunks) {
			m.nav.diffCursor = i
			return
		}
	}
}

// moveDiffCursorUp moves the diff cursor to the previous non-divider line.
// when moving up from a diff line, if the previous line has an annotation, lands on the annotation first.
// in collapsed mode, also skips removed lines unless their hunk is expanded.
func (m *Model) moveDiffCursorUp() {
	m.moveDiffCursorUpWithHunks(m.findHunks())
}

// moveDiffCursorUpWithHunks is the hunks-precomputed variant of moveDiffCursorUp.
// See moveDiffCursorDownWithHunks for the rationale.
func (m *Model) moveDiffCursorUpWithHunks(hunks []int) {
	// if currently on annotation sub-line, move up to the diff line itself. the sub-row renders below
	// its diff line (rowOnAnnotationSubLine), so clearing the flag IS the upward move - unlike the
	// downward walk, where clearing without advancing diffCursor is no movement at all and spins
	// moveDiffCursorDownBy.
	if m.nav.onAnnotationRow {
		m.nav.onAnnotationRow = false
		return
	}

	for i := m.nav.diffCursor - 1; i >= 0; i-- {
		if m.file.lines[i].ChangeType == git.ChangeDivider || m.isCollapsedHidden(i, hunks) {
			continue
		}
		m.nav.diffCursor = i
		// if this line has an annotation, land on it (skip for delete-only placeholders)
		dl := m.file.lines[i]
		if m.hasAnnotation(dl) && !m.isDeleteOnlyPlaceholder(i, hunks) {
			m.nav.onAnnotationRow = true
		}
		return
	}
	// if we're at the first line and there's a file-level annotation, go to it
	if m.nav.diffCursor >= 0 && m.hasFileAnnotation() {
		m.nav.diffCursor = -1
	}
}

// moveDiffCursorPageDown moves the diff cursor down by one visual page.
// keeps the cursor's relative screen position stable by scrolling both
// cursor and viewport by the same amount.
func (m *Model) moveDiffCursorPageDown() {
	m.moveDiffCursorDownBy(m.pageRows())
}

// moveDiffCursorPageUp moves the diff cursor up by one visual page.
// keeps the cursor's relative screen position stable by scrolling both
// cursor and viewport by the same amount.
func (m *Model) moveDiffCursorPageUp() {
	m.moveDiffCursorUpBy(m.pageRows())
}

// pageRows returns how far a full-page motion advances, one screen less the
// configured overlap. the overlap is approximate rather than exact, and deviates in both
// directions: the walk stops on cursor positions and one position can span several rendered
// rows (a wrapped line, an annotation block), so a tall line at the page edge carries over
// more than requested when the walk rolls back off it, and less than requested - down to
// rows skipped unseen - when worthRollingBack accepts it whole. the scroll_diff_page_*
// actions reuse this distance on a pure viewport scroll, where it is exact.
// half-page motions do not subtract it - they already retain half a screen.
func (m Model) pageRows() int {
	return max(1, m.layout.viewport.Height-m.modes.pageOverlap)
}

// halfPageRows returns how far a half-page motion advances. the page overlap is not
// subtracted: a half page already retains half a screen.
func (m Model) halfPageRows() int {
	return max(1, m.layout.viewport.Height/2)
}

// moveDiffCursorHalfPageDown moves the diff cursor down by half a visual page.
// scrolls viewport by half page explicitly, matching vim/less ctrl+d behavior.
func (m *Model) moveDiffCursorHalfPageDown() {
	m.moveDiffCursorDownBy(m.halfPageRows())
}

// moveDiffCursorHalfPageUp moves the diff cursor up by half a visual page.
// scrolls viewport by half page explicitly, matching vim/less ctrl+u behavior.
func (m *Model) moveDiffCursorHalfPageUp() {
	m.moveDiffCursorUpBy(m.halfPageRows())
}

// moveDiffCursorDownBy advances the cursor by up to rows visual rows and
// scrolls by the same amount, so its on-screen row stays put. Dividers, wrap
// continuations and annotation rows all count as rows, and a step onto an
// annotation sub-row counts as progress. A last step that would overshoot is
// rolled back when what was walked so far is worth keeping.
func (m *Model) moveDiffCursorDownBy(rows int) {
	hunks := m.findHunks()
	offsets := m.cursorVisualOffsets(hunks, m.buildAnnotationSet())
	startY := m.cursorViewportYFromOffsets(offsets)
	walked := 0
	for {
		prevCursor := m.nav.diffCursor
		prevAnnot := m.nav.onAnnotationRow
		m.moveDiffCursorDownWithHunks(hunks)
		if m.nav.diffCursor == prevCursor && m.nav.onAnnotationRow == prevAnnot {
			break // no more movement possible (end of content)
		}
		delta := m.cursorViewportYFromOffsets(offsets) - startY
		// walking down, the delta grows by the height of the line being LEFT, not the one
		// arrived at: offsets[i] is that line's top row, so its own wrap and annotation rows
		// are only counted once the cursor steps past them
		if delta > rows && m.worthRollingBack(walked, rows) {
			m.nav.diffCursor = prevCursor
			m.nav.onAnnotationRow = prevAnnot
			break
		}
		walked = delta
		if delta >= rows {
			break
		}
	}
	actualDelta := m.cursorViewportYFromOffsets(offsets) - startY
	maxOffset := max(0, m.layout.viewport.TotalLineCount()-m.layout.viewport.Height)
	m.layout.viewport.SetYOffset(min(m.layout.viewport.YOffset+actualDelta, maxOffset))
	m.layout.viewport.SetContent(m.renderDiff())
}

// moveDiffCursorUpBy moves the cursor up by up to rows visual rows
// and scrolls the viewport by the cursor's actual visual delta so the on-screen
// row stays stable and the cursor never rises above the viewport.
// accounts for divider lines, wrap continuations, and annotation rows that occupy rendered space.
// transitions off an annotation sub-row count as real progress so the loop does not
// terminate early on annotated lines.
// a step that would carry the delta past rows is undone on the same terms as the
// downward walk: scrolling by a tall line's whole height skips rows that were never rendered.
func (m *Model) moveDiffCursorUpBy(rows int) {
	hunks := m.findHunks()
	offsets := m.cursorVisualOffsets(hunks, m.buildAnnotationSet())
	startY := m.cursorViewportYFromOffsets(offsets)
	walked := 0
	for {
		prevCursor := m.nav.diffCursor
		prevAnnot := m.nav.onAnnotationRow
		m.moveDiffCursorUpWithHunks(hunks)
		if m.nav.diffCursor == prevCursor && m.nav.onAnnotationRow == prevAnnot {
			break // no more movement possible (start of content)
		}
		delta := startY - m.cursorViewportYFromOffsets(offsets)
		// walking up, the delta grows by the height of the line ARRIVED at, including the
		// annotation block that renders below it - the mirror of the downward walk
		if delta > rows && m.worthRollingBack(walked, rows) {
			m.nav.diffCursor = prevCursor
			m.nav.onAnnotationRow = prevAnnot
			break
		}
		walked = delta
		if delta >= rows {
			break
		}
	}
	actualDelta := startY - m.cursorViewportYFromOffsets(offsets)
	m.layout.viewport.SetYOffset(max(0, m.layout.viewport.YOffset-actualDelta))
	m.layout.viewport.SetContent(m.renderDiff())
}

// worthRollingBack reports whether undoing an overshooting step leaves a useful scroll.
// a rollback trades landing past the requested page for landing short of it, worthwhile only
// when what is already walked is a real move. with a block taller than the page ahead of the
// cursor there is no selectable position inside it, so rolling back to a row or two would
// scroll the pane by almost nothing and the next press would take the same oversized step
// anyway - worse than simply taking it now. half a page is the bar, matching ctrl+d/ctrl+u.
// walked is 0 on the first step, so no rollback can fire there and the walk can never
// refuse to move.
func (m Model) worthRollingBack(walked, rows int) bool {
	return walked*2 >= rows
}

// moveDiffCursorToStart moves the diff cursor to the first selectable position.
// if a file-level annotation exists, the cursor goes to -1 (file annotation line).
func (m *Model) moveDiffCursorToStart() {
	m.nav.onAnnotationRow = false
	if m.hasFileAnnotation() {
		m.nav.diffCursor = -1
		m.syncViewportToCursor()
		return
	}

	m.skipInitialDividers()
	m.syncViewportToCursor()
}

// moveDiffCursorToEnd moves the diff cursor to the last visible non-divider line.
// in collapsed mode, skips hidden removed lines.
func (m *Model) moveDiffCursorToEnd() {
	m.nav.onAnnotationRow = false
	hunks := m.findHunks()
	for i, dl := range slices.Backward(m.file.lines) {
		if dl.ChangeType != git.ChangeDivider && !m.isCollapsedHidden(i, hunks) {
			m.nav.diffCursor = i
			break
		}
	}
	m.syncViewportToCursor()
}

// syncViewportToCursor scrolls so the cursor line is visible with its whole
// visual extent, or at least its top row when it does not fit, and re-renders
// on the way. The content is set before the offset, because the viewport
// clamps the offset against the length of the content it currently holds.
func (m *Model) syncViewportToCursor() {
	// every layout change that widens the diff pane - resize, tree hide, line-number or
	// blame toggle - lands here before rendering, and each lowers the horizontal bound
	// without a horizontal keypress of its own.
	m.clampHorizontalScroll()
	cursorTop, cursorBottom := m.cursorVisualRange()
	m.layout.viewport.SetContent(m.renderDiff())
	switch {
	case cursorTop < m.layout.viewport.YOffset:
		m.layout.viewport.SetYOffset(cursorTop)
	case cursorBottom >= m.layout.viewport.YOffset+m.layout.viewport.Height:
		m.layout.viewport.SetYOffset(min(cursorBottom-m.layout.viewport.Height+1, cursorTop))
	}
}

// centerViewportOnCursor scrolls the viewport to place the cursor in the middle of the page.
// SetContent runs before SetYOffset so the viewport's clamp (bound to current content length)
// accepts the target offset even when the cursor move mutated render height (wrap/annotation rows).
func (m *Model) centerViewportOnCursor() {
	cursorY := m.cursorViewportY()
	offset := max(0, cursorY-m.layout.viewport.Height/2)
	m.layout.viewport.SetContent(m.renderDiff())
	m.layout.viewport.SetYOffset(offset)
}

// centerHunkInViewport centers the current hunk in the viewport.
// For small hunks, the entire hunk is centered. For large hunks that exceed
// the viewport height, the first line is placed near the top with a small context margin.
// No-op if the cursor is not on a changed line (ChangeAdd/ChangeRemove).
func (m *Model) centerHunkInViewport() {
	if m.nav.diffCursor < 0 || m.nav.diffCursor >= len(m.file.lines) {
		return
	}
	ct := m.file.lines[m.nav.diffCursor].ChangeType
	if ct != git.ChangeAdd && ct != git.ChangeRemove {
		return
	}

	// build shared context once for both cursor Y and hunk height
	var hunks []int
	if m.modes.collapsed.enabled {
		hunks = m.findHunks()
	}
	annotationSet := m.buildAnnotationSet()

	cursorY := m.cursorViewportYUsing(hunks, annotationSet)

	// find hunk end: scan forward from cursor while lines are changed.
	// hidden ChangeRemove lines (collapsed mode) are included in the range because
	// hunkLineHeight returns 0 for them, so over-extending hunkEnd is harmless.
	hunkEnd := m.nav.diffCursor
	for i := m.nav.diffCursor + 1; i < len(m.file.lines); i++ {
		ct := m.file.lines[i].ChangeType
		if ct != git.ChangeAdd && ct != git.ChangeRemove {
			break
		}
		hunkEnd = i
	}

	// calculate visual height of the hunk
	hunkVisualHeight := 0
	for i := m.nav.diffCursor; i <= hunkEnd; i++ {
		hunkVisualHeight += m.hunkLineHeight(i, hunks, annotationSet)
	}

	var offset int
	if hunkVisualHeight >= m.layout.viewport.Height {
		// hunk taller than viewport: place first line near top with small context margin
		offset = max(0, cursorY-2)
	} else {
		// center the entire hunk by centering its midpoint
		hunkMidY := cursorY + hunkVisualHeight/2
		offset = max(0, hunkMidY-m.layout.viewport.Height/2)
	}
	m.layout.viewport.SetYOffset(offset)
	m.layout.viewport.SetContent(m.renderDiff())
}

// topAlignViewportOnCursor scrolls the viewport to place the cursor at the top of the page.
// SetContent runs before SetYOffset - see centerViewportOnCursor for the clamp rationale.
func (m *Model) topAlignViewportOnCursor() {
	cursorY := m.cursorViewportY()
	m.layout.viewport.SetContent(m.renderDiff())
	m.layout.viewport.SetYOffset(max(0, cursorY))
}

// bottomAlignViewportOnCursor scrolls the viewport to place the cursor on the last visible row.
// mirror of topAlignViewportOnCursor with the offset flipped by viewport height.
// SetContent runs before SetYOffset - see centerViewportOnCursor for the clamp rationale.
func (m *Model) bottomAlignViewportOnCursor() {
	cursorY := m.cursorViewportY()
	m.layout.viewport.SetContent(m.renderDiff())
	m.layout.viewport.SetYOffset(max(0, cursorY-m.layout.viewport.Height+1))
}

// clampViewportRow clamps an absolute content row to the currently visible
// window [YOffset, YOffset+Height-1] so screen-position motions never target a
// row outside the screen (which would force a scroll). Height 0 collapses the
// window to the single YOffset row.
func (m *Model) clampViewportRow(row int) int {
	top := m.layout.viewport.YOffset
	bottom := top + max(m.layout.viewport.Height-1, 0)
	return min(max(row, top), bottom)
}

// nudgeOffDividerTowardViewportInterior nudges the cursor off a divider toward
// the interior of the visible window relative to targetRow. screen-position
// motions clamp their target into the window, but a divider sitting on the
// clamped row would otherwise nudge in a fixed direction that can leave the
// window (e.g. a large-count H clamped to the bottom row nudging further down).
// preferring the side with more room keeps the cursor on-screen.
func (m *Model) nudgeOffDividerTowardViewportInterior(targetRow int) {
	top := m.layout.viewport.YOffset
	bottom := top + max(m.layout.viewport.Height-1, 0)
	m.nudgeCursorOffDivider(targetRow-top <= bottom-targetRow)
}

// moveDiffCursorToScreenTop moves the cursor to the Nth visible line from the
// top of the viewport. n=0 or n=1 targets the first visible line. counts past
// the bottom of the screen clamp to the last visible line. Matches vim H.
func (m *Model) moveDiffCursorToScreenTop(n int) {
	if len(m.file.lines) == 0 {
		return
	}
	offset := max(0, n-1)
	row := m.clampViewportRow(m.layout.viewport.YOffset + offset)
	idx, onAnn := m.visualRowToDiffLine(row)
	m.nav.onAnnotationRow = onAnn
	m.nav.diffCursor = idx
	m.nudgeOffDividerTowardViewportInterior(row)
	m.adjustCursorIfHidden()
	m.syncViewportToCursor()
}

// moveDiffCursorToScreenMiddle moves the cursor to the middle visible line
// of the viewport. Matches vim M.
func (m *Model) moveDiffCursorToScreenMiddle() {
	if len(m.file.lines) == 0 {
		return
	}
	row := m.clampViewportRow(m.layout.viewport.YOffset + m.layout.viewport.Height/2)
	idx, onAnn := m.visualRowToDiffLine(row)
	m.nav.onAnnotationRow = onAnn
	m.nav.diffCursor = idx
	m.nudgeOffDividerTowardViewportInterior(row)
	m.adjustCursorIfHidden()
	m.syncViewportToCursor()
}

// moveDiffCursorToScreenBottom moves the cursor to the Nth visible line from the
// bottom of the viewport. n=0 or n=1 targets the last visible line. Matches vim L.
func (m *Model) moveDiffCursorToScreenBottom(n int) {
	if len(m.file.lines) == 0 {
		return
	}
	offset := max(0, n-1)
	row := m.clampViewportRow(m.layout.viewport.YOffset + m.layout.viewport.Height - 1 - offset)
	idx, onAnn := m.visualRowToDiffLine(row)
	m.nav.onAnnotationRow = onAnn
	m.nav.diffCursor = idx
	m.nudgeOffDividerTowardViewportInterior(row)
	m.adjustCursorIfHidden()
	m.syncViewportToCursor()
}

// nudgeCursorOffDivider pushes the cursor off a ChangeDivider row to the nearest
// non-divider line, since dividers cannot host cursor actions. preferForward
// searches forward (down) first so screen-position motions keep the cursor inside
// the viewport (forward from the top, backward from the bottom), otherwise it
// searches backward first. The opposite direction is the fallback, so the cursor
// still moves off a divider when the preferred side has no plain line.
func (m *Model) nudgeCursorOffDivider(preferForward bool) {
	if m.nav.diffCursor < 0 || m.nav.diffCursor >= len(m.file.lines) {
		return
	}
	if m.file.lines[m.nav.diffCursor].ChangeType != git.ChangeDivider {
		return
	}
	first, second := -1, 1 // backward first, forward fallback
	if preferForward {
		first, second = 1, -1
	}
	if m.scanForNonDivider(first) {
		return
	}
	m.scanForNonDivider(second)
}

// scanForNonDivider walks the diff lines from the current cursor in the given
// step direction (+1 forward, -1 backward) and moves the cursor to the first
// non-divider line found, returning true when one is found.
func (m *Model) scanForNonDivider(step int) bool {
	for i := m.nav.diffCursor + step; i >= 0 && i < len(m.file.lines); i += step {
		if m.file.lines[i].ChangeType != git.ChangeDivider {
			m.nav.diffCursor = i
			return true
		}
	}
	return false
}

// jumpToLineN moves the diff cursor to line n (1-indexed), clamped to [1, total],
// then centers the viewport on the new cursor position. no-op when the diff is empty.
// in collapsed mode, the cursor is nudged to the nearest visible line so it
// cannot land on a hidden removed line. if the target lands on a ChangeDivider row
// (e.g. G on a file with a trailing gap divider), the cursor nudges to the nearest
// non-divider line (backward first, then forward) so users can act on the landing.
func (m *Model) jumpToLineN(n int) {
	total := len(m.file.lines)
	if total == 0 {
		return
	}
	if n < 1 {
		n = 1
	}
	if n > total {
		n = total
	}
	m.nav.onAnnotationRow = false
	m.nav.diffCursor = n - 1
	// nudge off ChangeDivider rows before resolving hidden lines so the final
	// landing is both actionable and visible (collapsed mode may hide the
	// nudged-to line). backward first matches the centered feel of G / <N>G.
	m.nudgeCursorOffDivider(false)
	m.adjustCursorIfHidden()
	m.centerViewportOnCursor()
}

// findHunks scans diffLines and returns a slice of hunk start indices.
// a hunk is a contiguous group of added/removed lines. the returned index
// is the first line of each such group.
func (m Model) findHunks() []int {
	var hunks []int
	inHunk := false
	for i, dl := range m.file.lines {
		isChange := dl.ChangeType == git.ChangeAdd || dl.ChangeType == git.ChangeRemove
		switch {
		case isChange && !inHunk:
			hunks = append(hunks, i)
			inHunk = true
		case !isChange:
			inHunk = false
		}
	}
	return hunks
}

// currentHunk returns the 1-based hunk index and total hunk count.
// returns non-zero hunk index only when the cursor is on a changed line (add/remove).
// returns (0, total) when cursor is not inside any hunk.
func (m Model) currentHunk() (int, int) {
	hunks := m.findHunks()
	if len(hunks) == 0 {
		return 0, 0
	}
	if m.nav.diffCursor < 0 || m.nav.diffCursor >= len(m.file.lines) {
		return 0, len(hunks)
	}
	dl := m.file.lines[m.nav.diffCursor]
	if dl.ChangeType != git.ChangeAdd && dl.ChangeType != git.ChangeRemove {
		return 0, len(hunks)
	}
	// cursor is on a changed line, find which hunk
	cur := 0
	for i, start := range hunks {
		if m.nav.diffCursor >= start {
			cur = i + 1
		}
	}
	return cur, len(hunks)
}

// nearestHunkIndex returns the 0-based index of the change hunk starting at or
// before idx, or 0 when idx precedes the first hunk. Returns -1 when the file
// has no hunks. Used as the compact-toggle reposition fallback when the cursor
// sat on a context line that is absent from the re-fetched compact diff.
func (m Model) nearestHunkIndex(idx int) int {
	hunks := m.findHunks()
	if len(hunks) == 0 {
		return -1
	}
	nearest := 0
	for i, start := range hunks {
		if start <= idx {
			nearest = i
		}
	}
	return nearest
}

// moveToNextHunk moves the diff cursor to the start of the next change hunk.
// in collapsed mode, advances past hidden removed lines to the first visible line in the hunk.
func (m *Model) moveToNextHunk() {
	m.nav.onAnnotationRow = false
	hunks := m.findHunks()
	for _, start := range hunks {
		if start <= m.nav.diffCursor {
			continue
		}
		target := m.firstVisibleInHunk(start, hunks)
		if target < 0 {
			continue // skip delete-only hunks in collapsed mode
		}
		m.nav.diffCursor = target
		m.centerHunkInViewport()
		return
	}
}

// moveToPrevHunk moves the diff cursor to the start of the previous change hunk.
// in collapsed mode, advances past hidden removed lines to the first visible line in the hunk.
func (m *Model) moveToPrevHunk() {
	m.nav.onAnnotationRow = false
	hunks := m.findHunks()
	for _, h := range slices.Backward(hunks) {
		target := m.firstVisibleInHunk(h, hunks)
		if target < 0 {
			continue // skip delete-only hunks in collapsed mode
		}
		if target < m.nav.diffCursor {
			m.nav.diffCursor = target
			m.centerHunkInViewport()
			return
		}
	}
}

// handleHunkNav moves to the next or previous hunk, crossing file boundaries when needed.
// when cross-file hunk navigation is enabled, forward at the last hunk navigates to the next file
// and lands on its first hunk, and backward at the first hunk navigates to the previous file and
// lands on its last hunk.
// always shifts focus to the diff pane. no-op when no file is loaded.
func (m Model) handleHunkNav(forward bool) (tea.Model, tea.Cmd) {
	if m.file.name == "" {
		return m, nil
	}
	m.layout.focus = paneDiff
	prevCursor := m.nav.diffCursor
	if forward {
		m.moveToNextHunk()
	} else {
		m.moveToPrevHunk()
	}
	if m.nav.diffCursor != prevCursor || m.file.singleFile || !m.session.crossFileHunks {
		return m, nil
	}
	// the cursor did not move: this is the last (first) hunk of the file. the
	// tree is review mode's, so crossing to the next file happens there. commit
	// mode crosses its own file list the same way, see CommitModel.hunkNav
	return m.crossFileHunkNav(forward)
}

// computeLineWidths returns the rendered display width of every diff line, parallel to
// file.lines. it measures what applyHorizontalScroll actually cuts: the change prefix plus
// the tab-expanded content, gutters excluded (those shrink the visible width instead).
// measured on plain Content rather than the highlighted copy - chroma adds only ANSI, which
// carries no display width and costs more to scan.
func (m Model) computeLineWidths() []int {
	widths := make([]int, len(m.file.lines))
	for i, dl := range m.file.lines {
		prefix := changePrefixWidth
		if dl.ChangeType == git.ChangeDivider {
			prefix = dividerPrefixWidth
		}
		widths[i] = prefix + lipgloss.Width(strings.ReplaceAll(dl.Content, "\t", m.cfg.tabSpaces))
	}
	return widths
}

// maxHorizontalScroll returns the largest scrollX offset that still shows content, i.e. the
// widest rendered row minus the columns the pane can display. 0 when everything fits.
func (m Model) maxHorizontalScroll() int {
	visible := m.diffContentWidth() - m.gutterExtra()
	if visible <= 0 {
		return 0
	}
	return max(0, m.maxRenderedContentWidth()-visible)
}

// ensureLineWidths repopulates the width cache when it has fallen out of step with file.lines.
// lineWidths is pure derived data, so a miss must cost a scan and never correctness - a caller
// that sets file.lines without recomputing would otherwise get a zero bound and no horizontal
// scroll at all. length equality cannot catch a same-length replacement, so production still
// owes the recompute in handleFileLoaded. this only keeps a miss from being silent.
func (m *Model) ensureLineWidths() {
	if len(m.file.lineWidths) != len(m.file.lines) {
		m.file.lineWidths = m.computeLineWidths()
	}
}

// setScrollX stores a horizontal offset bounded to the current rendered document. every
// nonzero write to layout.scrollX must go through here: an unbounded offset past the widest
// row makes applyHorizontalScroll cut past every line, blanking the pane with no overflow indicator
// to explain it. the two direct `scrollX = 0` assignments (file load, wrap enable) are safe
// as they are.
func (m *Model) setScrollX(x int) {
	m.ensureLineWidths()
	m.layout.scrollX = min(max(0, x), m.maxHorizontalScroll())
}

// clampHorizontalScroll re-applies the bound to the stored offset. widening the visible area
// - a terminal resize, hiding the tree, turning off line numbers or blame - lowers the
// maximum without any horizontal keypress, so the paths that do those call this before they
// render.
func (m *Model) clampHorizontalScroll() {
	m.setScrollX(m.layout.scrollX)
}

// handleHorizontalScroll processes left/right scroll keys.
// direction < 0 scrolls left, direction > 0 scrolls right.
// no-op when wrap mode is active (content is already fully visible).
func (m *Model) handleHorizontalScroll(direction int) {
	if m.modes.wrap {
		return
	}
	if direction < 0 {
		m.setScrollX(m.layout.scrollX - scrollStep)
	} else {
		m.setScrollX(m.layout.scrollX + scrollStep)
	}
	m.layout.viewport.SetContent(m.renderDiff())
}

// scrollDiffViewportLine scrolls the diff viewport by delta lines, pinning the
// hidden cursor back into view. No-op if the offset can't change.
func (m *Model) scrollDiffViewportLine(delta int) {
	if !m.scrollDiffViewportBy(delta) {
		return
	}
	if m.pinDiffCursorTo(m.layout.viewport.YOffset) {
		m.layout.viewport.SetContent(m.renderDiff())
	}
}

// handleDiffAction dispatches a resolved action when the diff pane is focused.
func (m Model) handleDiffAction(action keymap.Action) (tea.Model, tea.Cmd) {
	if m.handleDiffMovement(action) {
		return m, nil
	}

	switch action {
	case keymap.ActionFocusTree:
		return m.handleSwitchToTree()
	case keymap.ActionScrollLeft:
		m.handleHorizontalScroll(-1)
		return m, nil
	case keymap.ActionScrollRight:
		m.handleHorizontalScroll(1)
		return m, nil
	case keymap.ActionScrollCenter:
		m.centerViewportOnCursor()
		return m, nil
	case keymap.ActionScrollTop:
		m.topAlignViewportOnCursor()
		return m, nil
	case keymap.ActionScrollBottom:
		m.bottomAlignViewportOnCursor()
		return m, nil
	case keymap.ActionDeleteAnnotation:
		cmd := m.deleteAnnotation()
		return m, cmd
	case keymap.ActionToggleHunk:
		m.toggleHunkExpansion()
		return m, nil
	case keymap.ActionSearch:
		cmd := m.startSearch()
		return m, cmd
	default: // actions handled by handleKey (quit, toggle_pane, filter, etc.) - not repeated here
	}
	return m, nil
}

func (m *Model) handleDiffMovement(action keymap.Action) bool {
	switch action {
	case keymap.ActionDown:
		m.moveDiffCursorDown()
		m.syncViewportToCursor()
	case keymap.ActionUp:
		m.moveDiffCursorUp()
		m.syncViewportToCursor()
	case keymap.ActionPageDown:
		m.moveDiffCursorPageDown()
	case keymap.ActionHalfPageDown:
		m.moveDiffCursorHalfPageDown()
	case keymap.ActionPageUp:
		m.moveDiffCursorPageUp()
	case keymap.ActionHalfPageUp:
		m.moveDiffCursorHalfPageUp()
	case keymap.ActionScrollDiffDown:
		m.scrollDiffViewportLine(wheelStep)
	case keymap.ActionScrollDiffUp:
		m.scrollDiffViewportLine(-wheelStep)
	case keymap.ActionScrollDiffPageDown:
		m.scrollDiffViewportLine(m.pageRows())
	case keymap.ActionScrollDiffPageUp:
		m.scrollDiffViewportLine(-m.pageRows())
	case keymap.ActionScrollDiffHalfPageDown:
		m.scrollDiffViewportLine(m.halfPageRows())
	case keymap.ActionScrollDiffHalfPageUp:
		m.scrollDiffViewportLine(-m.halfPageRows())
	case keymap.ActionHome:
		m.moveDiffCursorToStart()
	case keymap.ActionEnd:
		m.moveDiffCursorToEnd()
	default:
		return false
	}
	return true
}

// treePageSize returns the number of visible lines in the tree pane.
func (m Model) treePageSize() int {
	return max(1, m.paneHeight())
}

// paneHeight returns the content height for panes (total minus borders and status bar).
func (m Model) paneHeight() int {
	h := m.layout.height - 2 // borders
	if !m.cfg.noStatusBar {
		h-- // status bar
	}
	return max(1, h)
}

// positionOnFirstChange puts the cursor on the first changed line, falling back to the first visible
// line when the file carries no hunks at all (context-only sources). in collapsed mode it lands on
// the delete-only placeholder rather than skipping the hunk, since that head line stays visible.
//
// this only positions the cursor: a caller loading a new file MUST follow it with
// centerViewportOnCursor and must not drop that call as a duplicate render. moveToNextHunk scrolls
// via centerHunkInViewport, which sets the offset before rendering, so the offset clamps against the
// previously loaded file's length. on the no-hunk path nothing renders at all.
func (m *Model) positionOnFirstChange() {
	m.nav.diffCursor = -1
	m.moveToNextHunk()
	if m.nav.diffCursor == -1 {
		m.skipInitialDividers()
	}
}

// applyPendingHunkJump moves the cursor to the first or last hunk after a cross-file navigation.
func (m *Model) applyPendingHunkJump() {
	forward := *m.nav.pendingHunkJump
	m.nav.pendingHunkJump = nil
	if forward {
		m.positionOnFirstChange()
		return
	}

	m.nav.diffCursor = len(m.file.lines)
	m.moveToPrevHunk()
	if m.nav.diffCursor == len(m.file.lines) {
		m.skipInitialDividers()
	}
}
