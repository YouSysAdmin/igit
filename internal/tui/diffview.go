package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui/style"
	"github.com/yousysadmin/igit/internal/tui/worddiff"
)

// lineNumGutterWidth returns the total character width of the line number gutter.
// two-column layout: " " + oldNum(W) + " " + newNum(W) = 2*W + 2
// single-column layout: " " + num(W) = W + 1
func (m Model) lineNumGutterWidth() int {
	if m.file.singleColLineNum {
		return m.file.lineNumWidth + 1
	}
	return m.file.lineNumWidth*2 + 2
}

// lineNumGutter returns the formatted line number gutter string for a diff line.
// uses muted color via lipgloss style (StyleKeyLineNumber). safe here because the gutter
// is concatenated before content, so the lipgloss reset doesn't break outer backgrounds.
// two-column layout: " OOO NNN" where OOO is right-aligned old num, NNN is right-aligned new num.
// blank columns for adds (no old), removes (no new), and dividers (both blank).
// single-column layout: " NNN" - used for full-context files where OldNum == NewNum.
func (m Model) lineNumGutter(dl git.DiffLine) string {
	w := m.file.lineNumWidth
	blank := strings.Repeat(" ", w)

	if m.file.singleColLineNum {
		var col string
		if dl.ChangeType == git.ChangeDivider {
			col = blank
		} else {
			col = fmt.Sprintf("%*d", w, dl.NewNum)
		}
		return m.resolver.Style(style.StyleKeyLineNumber).Render(" " + col)
	}

	var oldCol, newCol string
	switch dl.ChangeType {
	case git.ChangeDivider:
		oldCol, newCol = blank, blank
	case git.ChangeAdd:
		oldCol = blank
		newCol = fmt.Sprintf("%*d", w, dl.NewNum)
	case git.ChangeRemove:
		oldCol = fmt.Sprintf("%*d", w, dl.OldNum)
		newCol = blank
	default: // context
		oldCol = fmt.Sprintf("%*d", w, dl.OldNum)
		newCol = fmt.Sprintf("%*d", w, dl.NewNum)
	}

	gutter := " " + oldCol + " " + newCol
	return m.resolver.Style(style.StyleKeyLineNumber).Render(gutter)
}

// blameGutterWidth returns the total character width of the blame gutter.
// layout: " " + author(W) + " " + age(3) = W + 5
func (m Model) blameGutterWidth() int {
	return m.file.blameAuthorLen + 5
}

// blameGutter returns the formatted blame gutter string for a diff line.
// shows author name (truncated) and relative age for lines with NewNum. blank for removed lines and dividers.
// now is the reference time for computing relative age, passed from the render entry point.
func (m Model) blameGutter(dl git.DiffLine, now time.Time) string {
	w := m.file.blameAuthorLen
	totalW := m.blameGutterWidth()
	blank := strings.Repeat(" ", totalW)

	lineNum := dl.NewNum
	if lineNum == 0 || dl.ChangeType == git.ChangeDivider {
		return m.resolver.Style(style.StyleKeyLineNumber).Render(blank)
	}

	bl, ok := m.file.blameData[lineNum]
	if !ok {
		return m.resolver.Style(style.StyleKeyLineNumber).Render(blank)
	}

	author := runewidth.Truncate(bl.Author, w, "…")
	pad := w - runewidth.StringWidth(author)
	if pad > 0 {
		author += strings.Repeat(" ", pad)
	}

	age := git.RelativeAge(bl.Time, now)
	gutter := " " + author + " " + age
	return m.resolver.Style(style.StyleKeyLineNumber).Render(gutter)
}

// hasBlameGutter returns true when the blame gutter should be rendered.
func (m Model) hasBlameGutter() bool {
	return m.modes.showBlame && len(m.file.blameData) > 0
}

// lineGutters returns the formatted line number and blame gutter strings for a diff line.
// returns empty strings for disabled gutters.
func (m Model) lineGutters(dl git.DiffLine) (numGutter, blameGutter string) {
	if m.modes.lineNumbers {
		numGutter = m.lineNumGutter(dl)
	}
	if m.hasBlameGutter() {
		blameGutter = m.blameGutter(dl, m.blameNow)
	}
	return numGutter, blameGutter
}

// gutterExtra returns the total character width consumed by enabled gutters (line numbers + blame).
func (m Model) gutterExtra() int {
	w := 0
	if m.modes.lineNumbers {
		w += m.lineNumGutterWidth()
	}
	if m.hasBlameGutter() {
		w += m.blameGutterWidth()
	}
	return w
}

// gutterBlanks returns blank strings matching the widths of enabled gutters,
// used as padding for wrap continuation lines.
func (m Model) gutterBlanks() (numBlank, blameBlank string) {
	if m.modes.lineNumbers {
		numBlank = strings.Repeat(" ", m.lineNumGutterWidth())
	}
	if m.hasBlameGutter() {
		blameBlank = strings.Repeat(" ", m.blameGutterWidth())
	}
	return numBlank, blameBlank
}

// applyHorizontalScroll cuts content to the diff content width at the current
// scroll offset and marks overflow with a glyph at each overflowing edge. The
// left glyph replaces the first visible column, the right one takes the last
// column plus the pane padding and is drawn on the pane background as chrome.
// indicatorBg is the line background behind the left glyph and the separating
// space. A pane too narrow for the glyphs falls back to a plain cut, and a
// non-positive content width returns the content unchanged.
func (m Model) applyHorizontalScroll(content string, indicatorBg style.Color) string {
	cutWidth := m.diffContentWidth() - m.gutterExtra()
	if cutWidth <= 0 {
		return content
	}
	origWidth := lipgloss.Width(content)
	start := m.layout.scrollX
	end := m.layout.scrollX + cutWidth

	hasLeftOverflow := start > 0 && origWidth > start
	hasRightOverflow := origWidth > end

	if !hasLeftOverflow && !hasRightOverflow {
		return ansi.Cut(content, start, end)
	}

	// reserve columns for indicators: left takes 1 visible col (replaces first col),
	// right reserves 1 col for a space separator and then extends 1 col beyond cutWidth
	// into the pane's right padding so the arrow sits flush against the border.
	innerStart := start
	innerEnd := end
	if hasLeftOverflow {
		innerStart++
	}
	if hasRightOverflow {
		innerEnd--
	}
	if innerEnd <= innerStart {
		// viewport too narrow to fit inner content plus indicators. fall back to plain cut
		return ansi.Cut(content, start, end)
	}

	var b strings.Builder
	if hasLeftOverflow {
		b.WriteString(m.leftScrollIndicator(indicatorBg))
	}
	b.WriteString(ansi.Cut(content, innerStart, innerEnd))
	if hasRightOverflow {
		b.WriteString(m.rightScrollIndicator(indicatorBg))
	}
	return b.String()
}

// plainHorizontalCut truncates content to the diff content width and applies horizontal scroll
// offset without emitting any overflow indicators. used for wrap-mode divider lines where
// indicators would contradict the "unwrapped mode only" design intent.
func (m Model) plainHorizontalCut(content string) string {
	cutWidth := m.diffContentWidth() - m.gutterExtra()
	if cutWidth <= 0 {
		return content
	}
	return ansi.Cut(content, m.layout.scrollX, m.layout.scrollX+cutWidth)
}

// leftScrollIndicator renders the left-side scroll overflow glyph using raw ANSI sequences
// so it doesn't break outer lipgloss backgrounds. the left indicator replaces the first visible content column,
// so it belongs to the line and uses lineBg for its background. empty string emits foreground only.
// in no-colors mode, falls back to reverse video.
func (m Model) leftScrollIndicator(lineBg style.Color) string {
	return m.scrollIndicatorANSI("«", lineBg, lineBg, false)
}

// rightScrollIndicator renders the right-side scroll overflow glyph prefixed with a space
// separator so the glyph doesn't touch the last content character. uses raw ANSI sequences so it
// doesn't break outer lipgloss backgrounds. the leading space carries lineBg so the colored line
// extends contiguously through the content area, while the right glyph itself is drawn on DiffBg so it
// visually sits on pane chrome (matching the surrounding right-padding column) rather than on the
// line's colored bg. in no-colors mode, falls back to reverse video.
func (m Model) rightScrollIndicator(lineBg style.Color) string {
	return m.scrollIndicatorANSI("»", lineBg, m.resolver.Color(style.ColorKeyDiffPaneBg), true)
}

// scrollIndicatorANSI builds the ANSI-encoded indicator string shared by left and right variants.
// leadingSpace controls whether a separator space is emitted before the glyph (drawn on spaceBg).
// glyphBg is the background for the glyph itself. the two can differ so the right indicator can
// keep its separator on the line bg (letting the colored content area extend naturally) while
// drawing its glyph on DiffBg to read as pane chrome. only emits the fg/bg reset sequences we
// actually set, so callers that pass an empty bg (or a theme with empty Muted) don't accidentally
// trash inherited pane background or foreground.
func (m Model) scrollIndicatorANSI(glyph string, spaceBg, glyphBg style.Color, leadingSpace bool) string {
	if m.cfg.noColors {
		prefix := ""
		if leadingSpace {
			prefix = " "
		}
		return prefix + "\033[7m" + glyph + "\033[27m"
	}
	var b strings.Builder
	if leadingSpace {
		if spaceBg != "" {
			b.WriteString(string(spaceBg))
		}
		b.WriteString(" ")
		if spaceBg != "" {
			b.WriteString("\033[49m")
		}
	}
	if glyphBg != "" {
		b.WriteString(string(glyphBg))
	}
	fg := m.resolver.Color(style.ColorKeyMutedFg)
	if fg != "" {
		b.WriteString(string(fg))
	}
	b.WriteString(glyph)
	if fg != "" {
		b.WriteString("\033[39m")
	}
	if glyphBg != "" {
		b.WriteString("\033[49m")
	}
	return b.String()
}

// renderDiff renders the current file's diff lines with styling, cursor highlight,
// and injected annotation lines.
func (m Model) renderDiff() string {
	if len(m.file.lines) == 0 {
		return "  no changes"
	}

	m.blameNow = time.Now()

	if m.modes.collapsed.enabled {
		return m.renderCollapsedDiff()
	}

	m.search.matchSet = m.buildSearchMatchSet()

	dec := m.decorations()
	var b strings.Builder
	m.renderFileAnnotationHeader(&b, dec)

	m.renderCache.rebase(m.globalRenderKey(dec), len(m.file.lines))
	// a large diff assembles into megabytes. without this the builder reallocates its way
	// there on every render. the previous render's size is the right estimate because
	// consecutive renders differ by a line or two.
	if n := m.renderCache.lastLen; n > 0 {
		b.Grow(n)
	}
	for i, dl := range m.file.lines {
		flags := m.lineRenderFlags(i, dec)
		if block, ok := m.renderCache.get(i, flags); ok {
			b.WriteString(block)
			continue
		}
		var lb strings.Builder
		m.renderDiffLine(&lb, i, dl, dec)
		m.renderAnnotationOrInput(&lb, i, dec)
		block := lb.String()
		m.renderCache.put(i, flags, block)
		b.WriteString(block)
	}
	m.renderCache.lastLen = b.Len()
	return b.String()
}

// globalRenderKey holds every comparable render input that is not per-line, so
// that a change to any of them misses the cache. Anything renderDiffLine or
// renderAnnotationOrInput reads beyond the line itself belongs here, with a
// matching state in the cache test. The style resolver, blame data, highlighted
// lines and intra-line ranges are not comparable and go through
// invalidateRenderCaches instead.
func (m Model) globalRenderKey(dec decorations) globalRenderKey {
	k := globalRenderKey{
		contentWidth: m.diffContentWidth(), scrollX: m.layout.scrollX,
		wrap: m.modes.wrap, lineNumbers: m.modes.lineNumbers,
		showBlame: m.modes.showBlame, wordDiff: m.modes.wordDiff,
		noColors: m.cfg.noColors, tabSpaces: m.cfg.tabSpaces, searchTerm: m.search.term,
		annotPrefix: m.session.annotPrefix, annotFilePrefix: m.session.annotFilePrefix,
		fileName: m.file.name, loadSeq: m.file.loadSeq,
		lineNumWidth: m.file.lineNumWidth, singleColLineNum: m.file.singleColLineNum,
		blameAuthorLen: m.file.blameAuthorLen,
		annotating:     dec.editor.active, fileAnnotating: dec.editor.onFile,
	}
	// the blame gutter renders a relative age, so its text drifts with wall time even
	// when nothing else moves. bucket to the minute: blame-off sessions never pay, and
	// a blame-on session rebuilds at most once a minute instead of showing frozen ages.
	if m.modes.showBlame {
		k.blameMinute = m.blameNow.Truncate(time.Minute).Unix()
	}
	return k
}

// lineRenderFlags captures the per-line state a cached block was rendered under.
func (m Model) lineRenderFlags(idx int, dec decorations) lineRenderFlags {
	f := lineRenderFlags{
		cursor:      m.isCursorLine(idx),
		selected:    dec.isSelected(idx),
		staged:      dec.isStaged(idx),
		searchMatch: m.search.matchSet[idx],
	}
	// the live input row carries textinput's own state (value, cursor position), which is
	// not reducible to a comparable key - mark it uncacheable rather than key on it.
	if dec.editsRow(idx, m.nav.diffCursor) {
		f.liveInput = true
		return f
	}
	if dec.empty() {
		return f
	}
	comment, has, remotes := m.decorationsFor(dec, m.file.lines[idx])
	if has {
		f.comment = comment
		f.hasComment = true
		f.annotCursor = idx == m.nav.diffCursor && dec.editor.onCursor && m.layout.focus == paneDiff
	}
	f.remotes = len(remotes)
	return f
}

// renderDiffLine writes a single styled diff line (with cursor highlight) to the builder.
// when wrap mode is active, long lines are broken at word boundaries with continuation markers.
func (m Model) renderDiffLine(b *strings.Builder, idx int, dl git.DiffLine, dec decorations) {
	lineContent, textContent, hasHighlight := m.prepareLineContent(idx, dl)
	textContent = m.applyIntraLineHighlight(idx, dl.ChangeType, textContent)
	isSearchMatch := m.search.matchSet[idx]

	isCursor := m.isCursorLine(idx)
	gutter := m.cursorGlyph(isCursor, dec.isSelected(idx), dec.isStaged(idx))

	// wrap mode: break long lines at word boundaries (dividers are short, skip them)
	if m.modes.wrap && dl.ChangeType != git.ChangeDivider {
		m.renderWrappedDiffLine(b, dl, textContent, hasHighlight, gutter, isSearchMatch)
		return
	}

	numGutter, blGutter := m.lineGutters(dl)

	var content string
	if dl.ChangeType == git.ChangeDivider {
		divider := m.resolver.Style(style.StyleKeyLineNumber)
		if dl.IsFileHeader {
			divider = m.resolver.Style(style.StyleKeyDirEntry).Bold(true)
		}
		content = divider.Render(" " + lineContent)
	} else {
		content = m.styleDiffContent(dl.ChangeType, m.linePrefix(dl.ChangeType), textContent, hasHighlight, isSearchMatch)
	}

	lineBg := m.resolver.LineBg(dl.ChangeType)
	// wrap mode divider fallthrough: dividers are unwrapped even in wrap mode, but indicators
	// only belong in unwrapped mode globally, so skip them for this edge case.
	if m.modes.wrap && dl.ChangeType == git.ChangeDivider {
		content = m.plainHorizontalCut(content)
	} else {
		content = m.applyHorizontalScroll(content, m.resolver.IndicatorBg(dl.ChangeType))
	}
	content = m.extendLineBg(content, lineBg)

	b.WriteString(gutter + numGutter + blGutter + content + "\n")
}

// isSelectedLine reports whether idx is inside the external selection hook or
// the review-mode visual range.
func (m Model) isSelectedLine(idx int) bool {
	return m.hostSelected(idx) || m.inVisualRange(idx)
}

// cursorGlyph returns the one-cell gutter marker: the cursor, the selection
// mark for other selected rows, the stage mark for planned rows, or a blank.
func (m Model) cursorGlyph(isCursor, selected, staged bool) string {
	switch {
	case isCursor:
		return m.renderer.DiffCursor(m.cfg.noColors)
	case selected:
		return m.renderer.SelectionMark(m.cfg.noColors)
	case staged:
		return m.renderer.StageMark(m.cfg.noColors)
	}
	return " "
}

// renderWrappedDiffLine renders a diff line with word wrapping, producing continuation lines with wrap markers.
// gutter is the one-cell cursor/selection glyph drawn on the first visual row.
func (m Model) renderWrappedDiffLine(b *strings.Builder, dl git.DiffLine, textContent string, hasHighlight bool, gutter string, isSearchMatch bool) {
	numGutter, blGutter := m.lineGutters(dl)
	numBlank, blBlank := m.gutterBlanks()

	visualLines := m.wrapContent(textContent, m.wrapWidth())
	for i, vl := range visualLines {
		ng := numBlank
		bg := blBlank
		var styled string
		if i == 0 {
			ng = numGutter
			bg = blGutter
			styled = m.styleDiffContent(dl.ChangeType, m.linePrefix(dl.ChangeType), vl, hasHighlight, isSearchMatch)
		} else {
			// non-collapsed wrap path applies search highlight per-word via
			// highlightSearchMatches inside styleDiffContent (the surrounding lipgloss
			// LineStyle stays add/remove/context, never SearchMatch), so the marker
			// does not need the no-colors reverse-video fallback even on a search-matched
			// row - pass false.
			styled = m.styledWrapMarker(m.resolver.LineBg(dl.ChangeType), false) + m.styleDiffContent(dl.ChangeType, "", vl, hasHighlight, isSearchMatch)
		}
		styled = m.extendLineBg(styled, m.resolver.LineBg(dl.ChangeType))

		cursor := " "
		if i == 0 {
			cursor = gutter
		}
		b.WriteString(cursor + ng + bg + styled + "\n")
	}
}

// wrappedLineCount returns the number of visual rows a diff line occupies.
// returns 1 when wrap mode is off or for divider lines.
// stays in sync with renderWrappedDiffLine by using the same wrapContent method and width calculation.
func (m Model) wrappedLineCount(idx int) int {
	if !m.modes.wrap || idx < 0 || idx >= len(m.file.lines) {
		return 1
	}
	dl := m.file.lines[idx]
	if dl.ChangeType == git.ChangeDivider {
		return 1
	}

	_, textContent, _ := m.prepareLineContent(idx, dl)
	return len(m.wrapContent(textContent, m.wrapWidth()))
}

// wrapContent wraps text content at the given width using word boundaries.
// returns a slice of visual lines (at least one). handles ANSI escape sequences.
// re-emits active SGR state (foreground, bold, italic) at the start of each continuation line
// because ansi.Wrap does not preserve ANSI state across inserted newlines.
func (m Model) wrapContent(content string, width int) []string {
	if width <= 0 {
		return []string{content}
	}
	wrapped := ansi.Wrap(content, width, "")
	lines := strings.Split(wrapped, "\n")
	if len(lines) <= 1 {
		return lines
	}
	return m.sgr.Reemit(lines)
}

// prepareLineContent returns the display-ready content for a diff line with tabs replaced.
// returns the raw line content, the best available content (highlighted if available), and whether highlight was used.
func (m Model) prepareLineContent(idx int, dl git.DiffLine) (lineContent, textContent string, hasHighlight bool) {
	lineContent = strings.ReplaceAll(dl.Content, "\t", m.cfg.tabSpaces)
	hasHighlight = idx < len(m.file.highlighted)
	textContent = lineContent
	if hasHighlight {
		textContent = strings.ReplaceAll(m.file.highlighted[idx], "\t", m.cfg.tabSpaces)
	}
	return lineContent, textContent, hasHighlight
}

// applyIntraLineHighlight inserts ANSI background markers for intra-line word-diff ranges.
// returns textContent unchanged when no intra-line ranges are available for the given line.
// uses WordAddBg/WordRemoveBg for color mode and reverse-video for no-color mode.
func (m Model) applyIntraLineHighlight(idx int, changeType git.ChangeType, textContent string) string {
	if idx >= len(m.file.intraRanges) || m.file.intraRanges[idx] == nil {
		return textContent
	}
	if changeType != git.ChangeAdd && changeType != git.ChangeRemove {
		return textContent
	}

	ranges := m.file.intraRanges[idx]
	if len(ranges) == 0 {
		return textContent
	}

	var hlOn, hlOff string
	if m.cfg.noColors {
		hlOn = "\033[7m"   // reverse video
		hlOff = "\033[27m" // reverse video off
	} else {
		switch changeType { //nolint:exhaustive // only add/remove relevant
		case git.ChangeAdd:
			hlOn = string(m.resolver.WordDiffBg(git.ChangeAdd))
			hlOff = string(m.resolver.LineBg(git.ChangeAdd)) // restore line bg
		case git.ChangeRemove:
			hlOn = string(m.resolver.WordDiffBg(git.ChangeRemove))
			hlOff = string(m.resolver.LineBg(git.ChangeRemove)) // restore line bg
		}
	}

	if hlOn == "" {
		return textContent
	}

	return m.differ.InsertHighlightMarkers(textContent, ranges, hlOn, hlOff)
}

// linePrefix returns the 3-character gutter prefix for a given change type.
func (m Model) linePrefix(changeType git.ChangeType) string {
	switch changeType {
	case git.ChangeAdd:
		return " + "
	case git.ChangeRemove:
		return " - "
	default:
		return "   "
	}
}

// wrapPrefixForHighlight wraps the +/-/~ prefix in explicit raw ANSI fg when
// chroma highlighting is on. Highlighted line styles intentionally set only
// background (chroma owns per-token fg for content), so the prefix would
// otherwise inherit the terminal default fg and may render invisibly on
// light theme backgrounds. An empty prefix is returned unchanged so callers
// passing "" (e.g. wrap-mode continuation rows that style the marker themselves)
// don't pay for an empty fg-set + fg-reset SGR pair on every row.
func (m Model) wrapPrefixForHighlight(prefix string, fg style.Color, hasHighlight bool) string {
	if !hasHighlight || fg == "" || prefix == "" {
		return prefix
	}
	return string(fg) + prefix + string(style.ResetFg)
}

// styledWrapMarker returns the wrap-continuation marker: a muted glyph on the
// line background, followed by effectiveWrapIndent padded spaces so
// continuation content hangs under the first row. An empty bg falls back to
// the pane background. searchMatch only matters with --no-colors, where the
// marker turns reverse-video to match the search-match row.
func (m Model) styledWrapMarker(bg style.Color, searchMatch bool) string {
	if m.cfg.noColors && searchMatch {
		indent := m.effectiveWrapIndent()
		pad := ""
		if indent > 0 {
			pad = strings.Repeat(" ", indent)
		}
		return "\033[7m ↪ " + pad + "\033[27m"
	}
	if bg == "" {
		bg = m.resolver.Color(style.ColorKeyDiffPaneBg)
	}
	muted := m.resolver.Color(style.ColorKeyMutedFg)
	indent := m.effectiveWrapIndent()
	var b strings.Builder
	if bg != "" {
		b.WriteString(string(bg))
	}
	if muted != "" {
		b.WriteString(string(muted))
	}
	b.WriteString(" ↪ ")
	if muted != "" {
		b.WriteString("\033[39m")
	}
	if indent > 0 {
		b.WriteString(strings.Repeat(" ", indent))
	}
	if bg != "" {
		b.WriteString("\033[49m")
	}
	return b.String()
}

// highlightSearchMatches wraps each occurrence of the search term in the visible text
// with ANSI background color sequence (preserving syntax foreground within matches).
// works with both plain text and ANSI-coded content by stripping ANSI to find match positions.
// changeType is used to restore the correct line background after each match (add/remove bg)
// instead of resetting to terminal default, which would break word-diff and line bg overlays.
func (m Model) highlightSearchMatches(s string, changeType git.ChangeType) string {
	if m.search.term == "" {
		return s
	}

	// find match positions in visible (ANSI-stripped) text
	plain := ansi.Strip(s)
	plainLower := strings.ToLower(plain)
	term := strings.ToLower(m.search.term)
	if !strings.Contains(plainLower, term) {
		return s
	}

	// collect all match ranges as byte offsets in ANSI-stripped text
	var matches []worddiff.Range
	offset := 0
	for {
		idx := strings.Index(plainLower[offset:], term)
		if idx < 0 {
			break
		}
		start := offset + idx
		matches = append(matches, worddiff.Range{Start: start, End: start + len(term)})
		offset = start + len(term)
	}
	if len(matches) == 0 {
		return s
	}

	// background-only highlight preserves syntax foreground colors within matches.
	// restore to line bg (add/remove) after each match instead of terminal default (\033[49m]),
	// so word-diff and line bg overlays are not broken by search highlights.
	searchBg := m.resolver.Color(style.ColorKeySearchBg)
	hlOn := string(searchBg)
	hlOff := "\033[49m"
	if hlOn == "" {
		// no-colors mode: fall back to reverse video so matches remain visible
		hlOn = "\033[7m"
		hlOff = "\033[27m"
	} else if bg := m.resolver.LineBg(changeType); bg != "" {
		hlOff = string(bg)
	}

	return m.differ.InsertHighlightMarkers(s, matches, hlOn, hlOff)
}

// styleDiffContent applies the appropriate line style based on change type.
// does NOT extend backgrounds - callers must apply extendLineBg after applyHorizontalScroll
// (non-wrap paths) or directly after styling (wrap paths where scroll is not used).
func (m Model) styleDiffContent(changeType git.ChangeType, prefix, content string, hasHighlight, isSearchMatch bool) string {
	if isSearchMatch && m.search.term != "" {
		content = m.highlightSearchMatches(content, changeType)
	}
	prefix = m.wrapPrefixForHighlight(prefix, m.resolver.LineFg(changeType), hasHighlight)
	return m.resolver.LineStyle(changeType, hasHighlight).Render(prefix + content)
}

// extendLineBg extends a styled line's background to the full diff content width
// using raw ANSI sequences. this ensures add/remove/modify backgrounds fill the entire line.
// subtracts line number gutter width when line numbers are enabled.
func (m Model) extendLineBg(styled string, bg style.Color) string {
	if bg == "" {
		return styled
	}
	// target = content area minus cursor bar (1) minus gutters (if on)
	// diffContentWidth() already excludes cursor bar. subtract gutters if enabled
	// diffContentWidth() already includes right padding, just subtract gutters
	targetWidth := m.diffContentWidth() - m.gutterExtra()
	currentWidth := lipgloss.Width(styled)
	if pad := targetWidth - currentWidth; pad > 0 {
		return styled + string(bg) + strings.Repeat(" ", pad) + "\033[49m"
	}
	return styled
}

const (
	wrapGutterWidth = 3  // wrap gutter prefix width: " + ", " - ", spaces, wrap marker
	wrapMinContent  = 10 // minimum content width per visual row when wrap-indent is active
)

// wrapWidth returns the width available to wrapped content: the diff content
// minus the gutters and, when wrapIndent is set, minus the indent that
// continuation rows prepend. Every visual row uses that same reduced width.
// An indent that would leave less than wrapMinContent is dropped for this
// render so wrap mode stays usable on a narrow pane.
func (m Model) wrapWidth() int {
	base := m.diffContentWidth() - wrapGutterWidth - m.gutterExtra()
	if base-m.cfg.wrapIndent < wrapMinContent {
		return base
	}
	return base - m.cfg.wrapIndent
}

// effectiveWrapIndent returns the wrap-indent that is actually applied for the
// current pane width. mirrors the clamp in wrapWidth so the styledWrapMarker's
// indent padding stays in sync - when wrapWidth disables the indent due to a
// narrow pane, the marker likewise emits no indent padding.
func (m Model) effectiveWrapIndent() int {
	base := m.diffContentWidth() - wrapGutterWidth - m.gutterExtra()
	if base-m.cfg.wrapIndent < wrapMinContent {
		return 0
	}
	return m.cfg.wrapIndent
}

// diffContentWidth returns the available width for diff line content.
// accounts for borders, cursor bar, and 1 char right padding to prevent text from touching the pane border.
func (m Model) diffContentWidth() int {
	if m.treePaneHidden() {
		// tree hidden or single file: diff pane borders (2) + cursor bar (1) + right padding (1)
		return max(10, m.layout.width-4)
	}
	// multi-file: diff pane width minus borders (4) minus tree width, minus bar (1), minus right padding (1)
	return max(10, m.layout.width-m.layout.treeWidth-4-2)
}

// annotLineKey identifies a line annotation for render-path lookups. Comparable on
// purpose: lineRenderFlags builds one per line on every render, including renders that
// are entirely cache hits, so the string form (annotationKey's Sprintf) allocated per
// line for any file carrying at least one annotation.
type annotLineKey struct {
	line       int
	changeType git.ChangeType
}

// globalRenderKey is the comparable fingerprint of non-per-line render state.
// It is compared by value, so every field must stay comparable.
type globalRenderKey struct {
	// contentWidth is the resolved diffContentWidth(), not the raw layout.width and
	// treeWidth it is computed from. Every width consumer in the render path
	// (applyHorizontalScroll, plainHorizontalCut, extendLineBg, wrapWidth,
	// annotationVisualRows) goes through diffContentWidth, and that branches on
	// treePaneHidden() = treeHidden || singleFile. Keying the raw
	// inputs missed those three: with treeWidth already 0 while the pane is shown -
	// reachable after a single-file diff becomes multi-file without treeWidth being
	// recomputed - pressing `t` moves no key field yet changes the width every line is
	// cut and padded to. Keying the resolved value cannot drift from what is consumed,
	// and matches how annotCacheKey already keys on the resolved wrapW.
	contentWidth int
	scrollX      int
	// layout.focus is deliberately NOT here. Every focus read inside the cached loop is the
	// `focus == paneDiff` term of isCursorLine and of the annotCursor condition, both captured
	// per line by lineRenderFlags, and both can only differ on the cursor line - so a focus
	// change dirties one line, not the file. Having it here made Tab and every click into the
	// tree drop the whole cache and pay a cold rebuild. The one remaining read, in
	// renderFileAnnotationHeader, is outside the loop and never cached.
	// layout.viewport.Width is likewise absent: nothing in the render path reads it at all.
	wrap        bool
	lineNumbers bool
	showBlame   bool
	wordDiff    bool
	noColors    bool
	annotating  bool
	// searchTerm, not just the per-line match bool: highlightSearchMatches locates the
	// highlighted byte range from the term, so two different terms matching the same set
	// of lines still paint differently.
	searchTerm       string
	fileAnnotating   bool
	singleColLineNum bool
	lineNumWidth     int
	blameAuthorLen   int
	blameMinute      int64
	loadSeq          uint64
	tabSpaces        string
	annotPrefix      string
	annotFilePrefix  string
	fileName         string
}

// lineRenderFlags is the per-line state a cached block was rendered under.
// hasComment distinguishes "no annotation" from "annotation with an empty body",
// which render differently.
type lineRenderFlags struct {
	cursor      bool
	selected    bool
	staged      bool // covered by the review stage plan
	searchMatch bool
	annotCursor bool
	hasComment  bool
	liveInput   bool
	comment     string
	remotes     int // remote comments painted under the line
}

// diffRenderCache memoizes each diff line's rendered block (the line plus any
// annotation rows below it). renderDiff has a value receiver, so the cache is
// held behind a pointer: every Model copy shares one instance, which is what
// lets a render populated by one copy serve the next.
//
// Sharing across copies is safe because entries are keyed by the state they were
// rendered under - identical inputs produce identical bytes, so a block written
// by a Model copy that was later discarded is still correct for any copy whose
// key matches.
type diffRenderCache struct {
	key     globalRenderKey
	blocks  []string
	flags   []lineRenderFlags
	filled  []bool
	lastLen int // byte length of the previous assembled render, used to pre-size the builder
}

// rebase drops everything when the global state or the line count changed, so a
// stale generation is never consulted.
func (c *diffRenderCache) rebase(key globalRenderKey, lines int) {
	if c.key == key && len(c.blocks) == lines {
		return
	}
	c.key = key
	c.blocks = make([]string, lines)
	c.flags = make([]lineRenderFlags, lines)
	c.filled = make([]bool, lines)
}

// get returns the cached block for a line when it was rendered under the same
// per-line state. The live annotation input row is never served from cache.
func (c *diffRenderCache) get(idx int, flags lineRenderFlags) (string, bool) {
	if flags.liveInput || idx < 0 || idx >= len(c.blocks) || !c.filled[idx] {
		return "", false
	}
	if c.flags[idx] != flags {
		return "", false
	}
	return c.blocks[idx], true
}

// put stores a rendered block. The live annotation input row is never stored.
func (c *diffRenderCache) put(idx int, flags lineRenderFlags, block string) {
	if flags.liveInput || idx < 0 || idx >= len(c.blocks) {
		return
	}
	c.blocks[idx] = block
	c.flags[idx] = flags
	c.filled[idx] = true
}

// invalidateRenderCaches clears the annotation row memo and the per-line
// render blocks. It is the only way to reflect the render inputs that no cache
// key can compare: the style resolver, blame data, highlighted lines and
// intra-line ranges. Any path that rebuilds one of them must call it. The
// comparable inputs invalidate through globalRenderKey on their own.
func (m *Model) invalidateRenderCaches() {
	m.clearAnnotationRows()
	m.renderCache.clear()
}

// clear drops every cached block, keeping the allocated slices.
func (c *diffRenderCache) clear() {
	clear(c.blocks)
	clear(c.filled)
	clear(c.flags)
	// the estimate belongs to content that is now gone. keeping it would pre-size the
	// builder for a 50k-line diff while rendering the 20-line file that replaced it
	c.lastLen = 0
}
