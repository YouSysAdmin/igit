package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/style"
)

// annotKeyFile is the lookup key for file-level annotations in wrappedAnnotationLineCount.
const annotKeyFile = "file"

// annotPrefix returns the cached annotation line prefix (marker + space).
func (m Model) annotPrefix() string {
	return m.session.annotPrefix
}

// annotFilePrefix returns the cached file-level annotation prefix (marker + " file: ").
func (m Model) annotFilePrefix() string {
	return m.session.annotFilePrefix
}

// annotCharLimit caps annotation text length. sized for multi-item lists and
// small pasted data slices, not for full-document content.
const annotCharLimit = 8000

// hunkKeywordRe matches whole-word "hunk" (case-insensitive). Looser words
// such as "block" are not keywords, they appear in casual prose too often.
var hunkKeywordRe = regexp.MustCompile(`(?i)\bhunk\b`)

// annotInputPrompt leads the first editor row.
const annotInputPrompt = "> "

// annotInputMaxRows caps the annotation editor height so a long multi-line
// comment scrolls inside the editor instead of swallowing the diff pane.
const annotInputMaxRows = 8

// newAnnotationInput creates and focuses a text area for annotation editing.
// prefixWidth accounts for the visible prefix characters (cursor col + emoji + label + margin).
func (m *Model) newAnnotationInput(placeholder string, prefixWidth int) (textarea.Model, tea.Cmd) {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.ShowLineNumbers = false
	ta.EndOfBufferCharacter = ' '
	// static cursor, set before Focus so no blink command is scheduled. the input is painted
	// inside renderDiff, so a blink could only become visible by re-rendering every diff line
	// (~7us per line) twice a second for a session that is otherwise idle. a static cursor is
	// what the user already sees between blinks on a large diff.
	ta.Cursor.SetMode(cursor.CursorStatic)
	cmd := ta.Focus()
	ta.CharLimit = annotCharLimit
	ta.MaxHeight = annotInputMaxRows
	// the prompt marks the editor row, continuation rows keep its width as
	// indent so wrapped text lines up under the first row. set before SetWidth,
	// which reads the prompt width to size the content area.
	ta.SetPromptFunc(len(annotInputPrompt), func(row int) string {
		if row == 0 {
			return annotInputPrompt
		}
		return strings.Repeat(" ", len(annotInputPrompt))
	})
	// prefixWidth already reserves the prompt columns, so SetWidth gets the
	// content width plus the prompt the editor draws itself
	ta.SetWidth(max(10, m.diffContentWidth()-prefixWidth) + len(annotInputPrompt))
	ta.SetHeight(1)

	// set DiffBg on all textarea sub-styles so View() output inherits the pane background.
	// wrapping View() externally doesn't work because lipgloss Render emits \033[0m resets.
	inputStyle := m.resolver.Style(style.StyleKeyAnnotInputText)
	ta.FocusedStyle.Base = inputStyle
	ta.FocusedStyle.Text = inputStyle
	ta.FocusedStyle.CursorLine = inputStyle
	ta.FocusedStyle.EndOfBuffer = inputStyle
	ta.FocusedStyle.Prompt = inputStyle
	cursorStyle := m.resolver.Style(style.StyleKeyAnnotInputCursor)
	ta.Cursor.TextStyle = cursorStyle
	ta.Cursor.Style = cursorStyle
	ta.FocusedStyle.Placeholder = m.resolver.Style(style.StyleKeyAnnotInputPlaceholder)

	return ta, cmd
}

// annotationInputRows returns how many rows the annotation editor occupies:
// every logical line wrapped at the editor width, capped at annotInputMaxRows.
// Both SetHeight and the viewport height math read this, so the painted rows
// and the scroll arithmetic cannot drift apart.
func (m Model) annotationInputRows(value string, width int) int {
	rows := 0
	for seg := range strings.SplitSeq(value, "\n") {
		if width > 0 && lipgloss.Width(seg) > width {
			rows += len(m.wrapContent(seg, width))
			continue
		}
		rows++
	}
	return min(max(rows, 1), annotInputMaxRows)
}

// syncAnnotationInputHeight resizes the editor to its current content.
func (m *Model) syncAnnotationInputHeight() {
	m.annot.input.SetHeight(m.annotationInputRows(m.annot.input.Value(), m.annot.input.Width()))
}

// startAnnotation enters annotation input mode for the current cursor line.
func (m *Model) startAnnotation() tea.Cmd {
	m.clearPendingInputState()
	dl, ok := m.cursorDiffLine()
	if !ok || dl.ChangeType == git.ChangeDivider {
		return nil
	}
	// prevent annotating hidden or placeholder removed lines in collapsed mode
	hunks := m.findHunks()
	if m.isCollapsedHidden(m.nav.diffCursor, hunks) {
		return nil
	}
	if m.isDeleteOnlyPlaceholder(m.nav.diffCursor, hunks) {
		return nil
	}

	// pre-fill with the existing annotation if one exists. multi-line comments
	// survive SetValue because the editor is a text area.
	lineNum := m.diffLineNum(dl)
	var preFill string
	for _, a := range m.store.Get(m.file.name) {
		if a.Line != lineNum || a.Type != string(dl.ChangeType) {
			continue
		}
		preFill = a.Comment
		break
	}

	ta, cmd := m.newAnnotationInput(m.annotationPlaceholder("annotation..."), 3+lipgloss.Width(m.annotPrefix())) // cursor col + annotation prefix + border margin
	if preFill != "" {
		ta.SetValue(preFill)
	}

	m.annot.input = ta
	m.annot.annotating = true
	m.annot.fileAnnotating = false
	m.syncAnnotationInputHeight()
	m.ensureLineAnnotationInputVisible()
	return cmd
}

// ensureLineAnnotationInputVisible scrolls the viewport so the line-annotation
// input row is visible. the input is rendered below the diff line, so keeping
// the cursor line visible is not always sufficient when cursor is on the last
// visible row.
func (m *Model) ensureLineAnnotationInputVisible() {
	if !m.annot.annotating || m.annot.fileAnnotating || m.layout.viewport.Height <= 0 {
		return
	}
	if m.nav.diffCursor < 0 || m.nav.diffCursor >= len(m.file.lines) {
		return
	}

	inputY := m.cursorViewportY() + m.wrappedLineCount(m.nav.diffCursor)
	// the editor grows downwards, so the bottom row is the one to keep in view
	lastY := inputY + m.annot.input.Height() - 1
	switch {
	case inputY < m.layout.viewport.YOffset:
		m.layout.viewport.SetYOffset(inputY)
	case lastY >= m.layout.viewport.YOffset+m.layout.viewport.Height:
		m.layout.viewport.SetYOffset(lastY - m.layout.viewport.Height + 1)
	}
}

// startFileAnnotation enters annotation input mode for a file-level annotation (Line=0).
func (m *Model) startFileAnnotation() tea.Cmd {
	m.clearPendingInputState()
	if m.file.name == "" {
		return nil
	}

	// pre-fill with the existing file-level annotation if one exists
	var preFill string
	for _, a := range m.store.Get(m.file.name) {
		if a.Line != 0 {
			continue
		}
		preFill = a.Comment
		break
	}

	ta, cmd := m.newAnnotationInput(m.annotationPlaceholder("file-level annotation..."), 3+lipgloss.Width(m.annotFilePrefix())) // cursor col + file annotation prefix + border margin
	if preFill != "" {
		ta.SetValue(preFill)
	}

	m.annot.input = ta
	m.annot.annotating = true
	m.annot.fileAnnotating = true
	m.syncAnnotationInputHeight()
	m.nav.diffCursor = -1 // position cursor on the file annotation line
	m.layout.viewport.GotoTop()
	return cmd
}

// saveAnnotation saves the current text input as an annotation on the cursor line.
// Thin wrapper around saveComment that reads model state for the current target.
func (m *Model) saveAnnotation() {
	text := m.annot.input.Value()
	if text == "" {
		m.cancelAnnotation()
		return
	}

	if m.annot.fileAnnotating {
		m.saveComment(text, m.file.name, true, 0, "")
		return
	}

	dl, ok := m.cursorDiffLine()
	if !ok {
		m.cancelAnnotation()
		return
	}
	m.saveComment(text, m.file.name, false, m.diffLineNum(dl), string(dl.ChangeType))
}

// saveComment persists the annotation text for the explicitly provided target.
// Target fields are taken as arguments (not read from model state) so the
// Enter-key path and the editor-finished path can both use it without
// temporal coupling on cursor position or current file. Hunk-end detection for
// line-level saves re-derives the diffLines index from the (line, changeType)
// pair so cursor movement during an external editor session does not skew the
// range. when fileName matches the currently loaded file, m.file.lines is
// scanned, otherwise EndLine expansion is skipped (no hunk context available).
func (m *Model) saveComment(text, fileName string, fileLevel bool, line int, changeType string) {
	if text == "" {
		m.cancelAnnotation()
		return
	}

	if fileLevel {
		m.store.Add(annot.Annotation{File: fileName, Line: 0, Type: "", Comment: text})
		m.annot.annotating = false
		m.annot.fileAnnotating = false
		m.nav.diffCursor = -1 // position cursor on the file annotation line
		m.tree.RefreshFilter(m.annotatedFiles())
		m.layout.viewport.SetContent(m.renderDiff())
		m.layout.viewport.GotoTop()
		return
	}

	a := annot.Annotation{File: fileName, Line: line, Type: changeType, Comment: text}
	if hunkKeywordRe.MatchString(text) && fileName == m.file.name {
		// re-derive the diff-line index from (line, changeType) so hunk-end
		// detection survives cursor drift during an external editor session.
		// only scan when the captured file still matches the loaded one -
		// otherwise m.file.lines describes a different file and would mislead.
		for i, dl := range m.file.lines {
			if string(dl.ChangeType) != changeType {
				continue
			}
			if m.diffLineNum(dl) != line {
				continue
			}
			if endLine := m.hunkEndLine(i); endLine > line {
				a.EndLine = endLine
			}
			break
		}
	}
	m.store.Add(a)
	m.annot.annotating = false
	m.annot.fileAnnotating = false // defensive hygiene: parity with file-level branch
	m.tree.RefreshFilter(m.annotatedFiles())
	// sync scroll so a newly added multi-row annotation stays visible when the
	// cursor sits near the bottom of the viewport.
	m.syncViewportToCursor()
}

// cancelAnnotation exits annotation input mode without saving.
func (m *Model) cancelAnnotation() {
	m.annot.annotating = false
	m.annot.fileAnnotating = false
	m.layout.viewport.SetContent(m.renderDiff())
}

// deleteFileAnnotation removes the file-level annotation and adjusts cursor position.
func (m *Model) deleteFileAnnotation() tea.Cmd {
	if !m.store.Delete(m.file.name, 0, "") {
		return nil
	}
	m.pendingAnnotJump = nil    // clear before refreshFilter which may trigger file load
	m.nav.pendingHunkJump = nil // clear before refreshFilter which may trigger file load
	m.skipInitialDividers()

	m.tree.RefreshFilter(m.annotatedFiles())

	if newFile := m.tree.SelectedFile(); newFile != "" && newFile != m.file.name {
		return m.requestFileDiff(newFile)
	}

	m.syncViewportToCursor()
	return nil
}

// deleteAnnotation removes the annotation of the cursor line, whether the
// cursor sits on the diff row or on the annotation row below it. The
// file-level annotation is deleted when the cursor is on its line. A line
// without an annotation only gets a hint. Returns a command to load the new
// file if the tree selection changed after the filter refresh.
func (m *Model) deleteAnnotation() tea.Cmd {
	if m.cursorOnFileAnnotationLine() {
		return m.deleteFileAnnotation()
	}

	dl, ok := m.cursorDiffLine()
	if !ok || dl.ChangeType == git.ChangeDivider {
		m.reload.hint = noAnnotationHint
		return nil
	}
	// a collapsed delete-only hunk hides its annotations, deleting what the
	// user cannot see would be a surprise
	if m.isDeleteOnlyPlaceholder(m.nav.diffCursor, m.findHunks()) {
		m.reload.hint = hiddenAnnotationHint
		return nil
	}

	lineNum := m.diffLineNum(dl)
	if !m.store.Delete(m.file.name, lineNum, string(dl.ChangeType)) {
		m.reload.hint = noAnnotationHint
		return nil
	}
	m.pendingAnnotJump = nil    // clear before refreshFilter which may trigger file load
	m.nav.pendingHunkJump = nil // clear before refreshFilter which may trigger file load
	m.nav.onAnnotationRow = false
	m.tree.RefreshFilter(m.annotatedFiles())

	// if filter moved cursor to a different file, load the new selection
	if newFile := m.tree.SelectedFile(); newFile != "" && newFile != m.file.name {
		return m.requestFileDiff(newFile)
	}

	m.syncViewportToCursor()
	return nil
}

// hints shown when the delete key finds nothing to remove.
const (
	noAnnotationHint     = "No annotation on this line"
	hiddenAnnotationHint = "Annotation hidden, expand the hunk to delete it"
)

// editorKeyDisplay returns the display name for the open_editor binding
// (e.g. "Ctrl+E") for use in placeholder text. Returns empty when unbound.
func (m Model) editorKeyDisplay() string {
	return m.actionKeyDisplay(keymap.ActionOpenEditor)
}

// actionKeyDisplay returns the display names bound to an action, joined with
// " / ". Chord bindings are filtered out since they don't fire during
// annotation input. Returns empty when the action is unbound.
func (m Model) actionKeyDisplay(action keymap.Action) string {
	var single []string
	for _, k := range m.keymap.KeysFor(action) {
		if leader, _, isChord := strings.Cut(k, ">"); !isChord || leader == "" {
			single = append(single, m.displayKeyName(k))
		}
	}
	return strings.Join(single, " / ")
}

// annotationPlaceholder appends the available assists to the prompt text:
// the newline key and the external editor key, each only when bound.
func (m Model) annotationPlaceholder(base string) string {
	var hints []string
	if key := m.actionKeyDisplay(keymap.ActionAnnotationNewline); key != "" {
		hints = append(hints, key+" for a line break")
	}
	if key := m.editorKeyDisplay(); key != "" {
		hints = append(hints, key+" for editor")
	}
	if len(hints) == 0 {
		return base
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(hints, ", "))
}

// handleAnnotateKey handles key messages during annotation input mode. The
// keymap is consulted first: alt+enter arrives as KeyEnter with the alt
// modifier set, so a plain msg.Type switch would save instead of inserting the
// line break.
func (m Model) handleAnnotateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.keymap.Resolve(msg.String()) { //nolint:exhaustive // only the two editor assists reach here, the rest is text
	case keymap.ActionOpenEditor:
		cmd := m.openEditor()
		return m, cmd
	case keymap.ActionAnnotationNewline:
		m.annot.input.InsertString("\n")
		m.syncAnnotationInputHeight()
		m.layout.viewport.SetContent(m.renderDiff())
		m.ensureLineAnnotationInputVisible()
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEnter:
		m.saveAnnotation()
		return m, nil
	case tea.KeyEsc:
		m.cancelAnnotation()
		return m, nil
	default:
		var cmd tea.Cmd
		m.annot.input, cmd = m.annot.input.Update(msg)
		m.syncAnnotationInputHeight()
		m.layout.viewport.SetContent(m.renderDiff()) // re-render so typed characters are visible immediately
		m.ensureLineAnnotationInputVisible()
		return m, cmd
	}
}

// cursorLineHasAnnotation checks if the cursor is on a deletable annotation line.
// returns true only when cursor is on the file annotation line or on an annotation sub-line.
func (m Model) cursorLineHasAnnotation() bool {
	return m.cursorOnFileAnnotationLine() || m.nav.onAnnotationRow
}

// clearAnnotationRows drops the memo of wrapped annotation rows. The render
// caches invalidate together, see invalidateRenderCaches.
func (m *Model) clearAnnotationRows() { clear(m.annot.rowCache) }

// hasAnnotation reports whether the diff line carries an annotation, honoring
// a host that hides them so navigation never lands on an invisible annotation.
func (m Model) hasAnnotation(dl git.DiffLine) bool {
	return !m.host.annotationsHidden && m.store.Has(m.file.name, m.diffLineNum(dl), string(dl.ChangeType))
}

// hasFileAnnotation checks if the current file has a file-level annotation
// (Line=0), honoring a host that hides them.
func (m Model) hasFileAnnotation() bool {
	if m.host.annotationsHidden {
		return false
	}
	for _, a := range m.store.Get(m.file.name) {
		if a.Line == 0 {
			return true
		}
	}
	return false
}

// hasFileRow reports whether the file block occupies rows: a file-level
// annotation, remote comments that belong to the file, or both. Layout and
// navigation ask this, the store-mutating paths keep asking hasFileAnnotation.
func (m Model) hasFileRow() bool {
	if m.hasFileAnnotation() {
		return true
	}
	return !m.host.annotationsHidden && len(m.remoteByKey[annotKeyFile]) > 0
}

// cursorOnFileAnnotationLine returns true if the diff cursor is on the file-level annotation line.
func (m Model) cursorOnFileAnnotationLine() bool {
	return m.nav.diffCursor == -1 && m.hasFileRow()
}

// diffLineNum returns the display line number for a diff line.
func (m Model) diffLineNum(dl git.DiffLine) int {
	if dl.ChangeType == git.ChangeRemove {
		return dl.OldNum
	}
	return dl.NewNum
}

// hunkEndLine returns the display line number of the last line in the change hunk
// containing diffLines[idx]. only walks forward through lines of the same change type
// as the starting line, so both start and end use the same number space (old or new).
// returns 0 if idx is not inside a change hunk.
func (m Model) hunkEndLine(idx int) int {
	if idx < 0 || idx >= len(m.file.lines) {
		return 0
	}
	dl := m.file.lines[idx]
	if dl.ChangeType != git.ChangeAdd && dl.ChangeType != git.ChangeRemove {
		return 0
	}

	// walk forward from idx to find the last contiguous line of the same change type
	startType := dl.ChangeType
	last := idx
	for i := idx + 1; i < len(m.file.lines); i++ {
		if m.file.lines[i].ChangeType != startType {
			break
		}
		last = i
	}
	return m.diffLineNum(m.file.lines[last])
}

// annotCacheKey is the lookup key for annotationState.rowCache. fields are
// the comparable inputs to the wrap+style pipeline: cache hits require all
// three to match. width self-invalidates: a different pane width produces a
// different key and triggers a fresh compute. field order matches the
// (prefix, body, width) API convention used by annotationVisualRows and
// composeAnnotationRows.
type annotCacheKey struct {
	prefix, body string
	width        int
}

// annotationPrefixBody resolves the (prefix, body) pair for the annotation
// identified by key. file-level annotations (key == annotKeyFile) get the
// file-level prefix. line-level annotations get the line prefix. returns ("", "")
// when no annotation matches the key.
func (m Model) annotationPrefixBody(key string) (prefix, body string) {
	for _, a := range m.store.Get(m.file.name) {
		if key == annotKeyFile && a.Line == 0 {
			return m.annotFilePrefix(), a.Comment
		}
		if key != annotKeyFile && m.annotationKey(a.Line, a.Type) == key {
			return m.annotPrefix(), a.Comment
		}
	}
	return "", ""
}

// annotSegment is one block of rows painted under a diff line: the marker that
// leads it and the text it carries.
type annotSegment struct {
	prefix string
	body   string
}

// annotationSegments lists the blocks under one line: the session's own
// annotation first when hasLocal, then one block per remote review comment.
// Every block gets its own marker, so a remote comment cannot read as a
// continuation of the local one.
func (m Model) annotationSegments(hasLocal bool, prefix, body string, remotes []RemoteNote) []annotSegment {
	segments := make([]annotSegment, 0, len(remotes)+1)
	if hasLocal {
		segments = append(segments, annotSegment{prefix: prefix, body: body})
	}
	for _, n := range remotes {
		segments = append(segments, annotSegment{prefix: m.remoteNotePrefix(), body: n.text()})
	}
	return segments
}

// annotationVisualRows is the single source of truth for how an annotation is
// painted: it returns the fully-styled visual rows for (prefix, body) at the
// current pane width. wrappedAnnotationLineCount uses len() of this. the
// painter iterates these rows directly. results are memoized on rowCache.
// invalidation is the caller's responsibility (handleFileLoaded, applyTheme,
// cancelThemeSelect). pointer receiver is mandatory: the method writes to
// m.annot.rowCache and the consistency with invalidateRenderCaches protects
// against future LRU/slice replacements that would silently no-op on a value
// receiver.
func (m *Model) annotationVisualRows(prefix, body string) []string {
	// 1 for cursor column. clamp to wrapMinContent so the cache key normalizes
	// tiny-pane widths that all produce identical no-wrap output.
	wrapW := max(m.diffContentWidth()-1, wrapMinContent)
	key := annotCacheKey{prefix: prefix, body: body, width: wrapW}
	if rows, ok := m.annot.rowCache[key]; ok {
		return rows
	}
	rows := m.composeAnnotationRows(prefix, body, wrapW)
	m.annot.rowCache[key] = rows
	return rows
}

// composeAnnotationRows builds the styled visual rows for an annotation.
// splits body on "\n" into logical lines, applies prefix on row 0 and a matching-width
// plain-space indent on continuation rows so body columns line up, wraps each segment
// at wrapW, and wraps each visual row in AnnotationInline.
//
// IMPORTANT: the returned rows bake in the resolver's AnnotationInline styling
// envelope. applyTheme rebuilds the resolver - invalidation of rowCache on
// applyTheme is load-bearing. anyone adding a runtime color toggle that affects
// AnnotationInline MUST also invalidate the cache.
func (m Model) composeAnnotationRows(prefix, body string, wrapW int) []string {
	// a bare carriage return returns the terminal cursor to the start of the
	// line and overwrites what was drawn there, so no body reaches the painter
	// carrying one. CRLF text arrives from request comments and from files
	// loaded with --annotations
	first := strings.ReplaceAll(prefix+body, "\r\n", "\n")
	first = strings.ReplaceAll(first, "\r", "\n")
	logical := strings.Split(first, "\n")
	indent := strings.Repeat(" ", lipgloss.Width(prefix))

	var rows []string
	for i, segment := range logical {
		if i > 0 {
			segment = indent + segment
		}
		var lines []string
		if wrapW > wrapMinContent && lipgloss.Width(segment) > wrapW {
			lines = m.wrapContent(segment, wrapW)
		} else {
			lines = []string{segment}
		}
		for _, line := range lines {
			rows = append(rows, m.renderer.AnnotationInline(line))
		}
	}
	return rows
}

// wrappedAnnotationLineCount returns the number of visual rows an annotation occupies.
// annotations always wrap at the pane width regardless of wrapMode.
// resolves (prefix, body) via annotationPrefixBody and defers to the chokepoint
// annotationVisualRows so the height query and the painter cannot drift apart.
// returns 1 when no annotation matches the key (preserves prior behavior). when an
// annotation exists with an empty body, the chokepoint produces a single prefix-only
// row, matching master's behavior for blank annotations preloaded via --annotations.
func (m *Model) wrappedAnnotationLineCount(key string) int {
	prefix, body := m.annotationPrefixBody(key)
	segments := m.annotationSegments(prefix != "" || body != "", prefix, body, m.remoteByKey[key])
	if len(segments) == 0 {
		return 1
	}
	rows := 0
	for _, s := range segments {
		rows += len(m.annotationVisualRows(s.prefix, s.body))
	}
	return rows
}

// hunkLineHeight returns the visual row count for a single diff line,
// including collapsed visibility, wrap, and inline annotation.
func (m Model) hunkLineHeight(idx int, hunks []int, annotationSet map[string]bool) int {
	if m.isCollapsedHidden(idx, hunks) {
		return 0
	}
	if m.isDeleteOnlyPlaceholder(idx, hunks) {
		return m.deletePlaceholderVisualHeight(idx)
	}
	h := m.wrappedLineCount(idx)
	dl := m.file.lines[idx]
	if dl.ChangeType != git.ChangeDivider {
		key := m.annotationKey(m.diffLineNum(dl), string(dl.ChangeType))
		if annotationSet[key] {
			h += m.wrappedAnnotationLineCount(key)
		}
	}
	return h
}

// cursorViewportY computes the actual viewport Y position of the cursor,
// accounting for injected annotation lines and the file-level annotation line.
// in collapsed mode, hidden removed lines (those in non-expanded hunks) are not counted.
func (m Model) cursorViewportY() int {
	var hunks []int
	if m.modes.collapsed.enabled {
		hunks = m.findHunks()
	}
	return m.cursorViewportYUsing(hunks, m.buildAnnotationSet())
}

// cursorViewportYUsing is the same as cursorViewportY but accepts pre-built
// hunks and annotationSet to avoid redundant computation when the caller
// already has them (e.g. centerHunkInViewport).
func (m Model) cursorViewportYUsing(hunks []int, annotationSet map[string]bool) int {
	if m.file.name == "" || len(m.file.lines) == 0 {
		return max(0, m.nav.diffCursor)
	}

	fileAnnotationOffset := 0
	if m.hasFileRow() {
		fileAnnotationOffset = m.wrappedAnnotationLineCount(annotKeyFile)
	}

	if m.nav.diffCursor == -1 {
		return 0
	}

	y := fileAnnotationOffset
	for i := 0; i < m.nav.diffCursor && i < len(m.file.lines); i++ {
		y += m.hunkLineHeight(i, hunks, annotationSet)
	}
	if m.nav.onAnnotationRow {
		y += m.wrappedLineCount(m.nav.diffCursor)
	}
	return y
}

// cursorVisualOffsets returns the top visual row for every diff line. Page
// motions build this index once so each cursor step can read its visual
// position in O(1) instead of rescanning all preceding lines.
func (m Model) cursorVisualOffsets(hunks []int, annotationSet map[string]bool) []int {
	offsets := make([]int, len(m.file.lines))
	y := 0
	if m.hasFileRow() {
		y = m.wrappedAnnotationLineCount(annotKeyFile)
	}
	for i := range m.file.lines {
		offsets[i] = y
		y += m.hunkLineHeight(i, hunks, annotationSet)
	}
	return offsets
}

// cursorViewportYFromOffsets returns the current cursor's visual row from a
// pre-built line-offset index. offsets must come from cursorVisualOffsets for
// the current m.file.lines, and m.nav.diffCursor must be in [-1, len(offsets)).
func (m Model) cursorViewportYFromOffsets(offsets []int) int {
	if m.file.name == "" || len(offsets) == 0 {
		return max(0, m.nav.diffCursor)
	}
	if m.nav.diffCursor == -1 {
		return 0
	}
	y := offsets[m.nav.diffCursor]
	if m.nav.onAnnotationRow {
		y += m.wrappedLineCount(m.nav.diffCursor)
	}
	return y
}

// cursorVisualRange returns the top and bottom viewport Y coordinates the
// cursor currently occupies. when the cursor is on a diff row, bottom spans
// any wrap-continuation rows plus any injected annotation rows below it.
// when the cursor is on an annotation sub-line, bottom spans only the
// annotation rows (the diff row sits above top). callers keeping the cursor
// "visible" use this range to preserve the full logical extent, not just the
// top row.
func (m Model) cursorVisualRange() (top, bottom int) {
	var hunks []int
	if m.modes.collapsed.enabled {
		hunks = m.findHunks()
	}
	annotationSet := m.buildAnnotationSet()
	top = m.cursorViewportYUsing(hunks, annotationSet)
	h := max(m.cursorVisualHeight(hunks, annotationSet), 1)
	return top, top + h - 1
}

// rowOnAnnotationSubLine reports whether relRow (0-based, relative to the first
// visual row of the diff line at idx) targets the injected annotation sub-line
// below that diff line. h is the total visual height of the diff line (wrap rows
// plus any injected annotation rows). delete-only placeholders always return
// false because renderCollapsedDiff skips annotation rendering for them, even
// when the underlying removed line has an annotation.
func (m Model) rowOnAnnotationSubLine(idx, relRow, h int, hunks []int, annSet map[string]bool) bool {
	if m.isDeleteOnlyPlaceholder(idx, hunks) {
		return false
	}
	dl := m.file.lines[idx]
	if dl.ChangeType == git.ChangeDivider {
		return false
	}
	key := m.annotationKey(m.diffLineNum(dl), string(dl.ChangeType))
	if !annSet[key] {
		return false
	}
	annRows := m.wrappedAnnotationLineCount(key)
	return annRows > 0 && relRow >= h-annRows
}

// visualRowToDiffLine maps a visual row of the diff viewport back to a
// diff-line index, the inverse of cursorVisualRange. row is relative to the
// first visible content row. Rows of a file-level annotation map to -1, and
// onAnnotation is true when the row belongs to an injected annotation line
// rather than to the diff line. Rows past the end clamp to the last line.
func (m Model) visualRowToDiffLine(row int) (idx int, onAnnotation bool) {
	if len(m.file.lines) == 0 {
		if m.hasFileRow() && row >= 0 && row < m.wrappedAnnotationLineCount(annotKeyFile) {
			return -1, false
		}
		return m.nav.diffCursor, false
	}

	var hunks []int
	if m.modes.collapsed.enabled {
		hunks = m.findHunks()
	}
	annSet := m.buildAnnotationSet()

	running := 0
	if m.hasFileRow() {
		fileRows := m.wrappedAnnotationLineCount(annotKeyFile)
		if row < fileRows {
			return -1, false
		}
		running = fileRows
	} else if row < 0 {
		// no file annotation, row above the top: pick the first visible line
		for i := range m.file.lines {
			if m.hunkLineHeight(i, hunks, annSet) > 0 {
				return i, false
			}
		}
		return 0, false
	}

	for i := range m.file.lines {
		h := m.hunkLineHeight(i, hunks, annSet)
		if h == 0 {
			continue
		}
		if row < running+h {
			return i, m.rowOnAnnotationSubLine(i, row-running, h, hunks, annSet)
		}
		running += h
	}
	// row past the last visible line: return the last visible line index
	for i := range slices.Backward(m.file.lines) {
		if m.hunkLineHeight(i, hunks, annSet) > 0 {
			return i, false
		}
	}
	return len(m.file.lines) - 1, false
}

// cursorVisualHeight returns how many visual rows the cursor occupies: the
// wrapped row count of the annotation it sits on, or the full height of the
// diff line including wrap and annotation rows. hunks and annotationSet must
// describe the current file.
func (m Model) cursorVisualHeight(hunks []int, annotationSet map[string]bool) int {
	if m.cursorOnFileAnnotationLine() {
		return m.wrappedAnnotationLineCount(annotKeyFile)
	}
	if m.nav.diffCursor < 0 || m.nav.diffCursor >= len(m.file.lines) {
		return 1
	}
	dl := m.file.lines[m.nav.diffCursor]
	if m.nav.onAnnotationRow {
		if dl.ChangeType == git.ChangeDivider {
			return 1
		}
		return m.wrappedAnnotationLineCount(m.annotationKey(m.diffLineNum(dl), string(dl.ChangeType)))
	}
	return m.hunkLineHeight(m.nav.diffCursor, hunks, annotationSet)
}

// buildAnnotationSet returns a set of annotation keys for the current file.
// excludes file-level annotations (Line=0) since they are rendered separately.
func (m Model) buildAnnotationSet() map[string]bool {
	annotations := m.store.Get(m.file.name)
	set := make(map[string]bool, len(annotations))
	for _, a := range annotations {
		if a.Line == 0 {
			continue
		}
		set[m.annotationKey(a.Line, a.Type)] = true
	}
	// a line carrying only remote comments still grows by their rows
	for key := range m.remoteByKey {
		set[key] = true
	}
	return set
}

// annotationKey creates a lookup key from line number and change type.
func (m Model) annotationKey(line int, changeType string) string {
	return fmt.Sprintf("%d:%s", line, changeType)
}
