package tui

import (
	"time"

	"github.com/yousysadmin/igit/internal/git"
)

// This file is the seam between the two modes' use of Model. Review mode owns
// its whole window and drives Model directly. Commit mode embeds a Model as the
// pane that renders its staging diff, and tells it the few things only the host
// knows: who has the focus, which rows the host has selected, and whether the
// diff's line numbers still refer to the working tree. Everything the host
// needs to say goes through paneHost and the setters below, so a later split of
// the pane into its own package has one contract to move instead of a handful
// of fields set from across the package.

// diffPane is the state of the pane that draws one file's diff: the viewport
// and where the cursor sits in it, the loaded rows, the display toggles, the
// search over them, the render cache, and what a host has told it. Review mode
// and commit mode each drive one.
//
// It is embedded in Model, so every existing call site keeps reaching these
// fields directly. The struct exists to say where the boundary runs: a field
// here is the pane's, everything left on Model is review mode's.
//
// Two members still straddle the line and are marked where they are declared:
// layoutState carries the review tree's width and hidden flag, modeState its
// show-untracked toggle. They move out when the pane becomes its own package.
type diffPane struct {
	// what it draws with
	resolver    styleResolver     // color and style lookups
	renderer    styleRenderer     // compound ANSI rendering
	sgr         sgrProcessor      // SGR stream reemit
	differ      wordDiffer        // intra-line diff and highlight insertion
	highlighter SyntaxHighlighter // syntax highlighting of the loaded rows
	styleGen    uint64            // bumped whenever resolver/renderer/sgr are replaced (theme change), lets App sync styles cheaply

	cfg        paneConfig      // the session settings the pane itself honors
	layout     layoutState     // viewport, focus and the pane's own size
	modes      modeState       // user-togglable view modes
	nav        navigationState // cursor and navigation
	file       loadedFileState // the loaded file's rows, highlights and blame
	search     searchState     // search lifecycle over the loaded rows
	wheel      wheelState      // mouse wheel coalescing (debounced render via wheelDebounceMsg)
	host       paneHost        // what an embedding host has told this pane
	headerNote string          // free text shown under the diff header (see setHeaderNote)
	blameNow   time.Time       // snapshot of time.Now() set once per render pass for blame age

	// renderCache memoizes per-line rendered blocks for renderDiff. Held behind a
	// pointer because renderDiff has a value receiver: every Model copy shares one
	// instance, which is what lets a block rendered by one copy serve the next.
	// NewModel initializes this. direct Model{} construction is unsupported.
	renderCache *diffRenderCache
}

// paneConfig is the slice of the session's settings the pane needs to draw.
// Everything else on modelConfigState is the host's.
type paneConfig struct {
	tabSpaces   string // spaces a tab expands to
	wrapIndent  int    // extra indent (columns) for wrap continuation rows, 0 disables
	noColors    bool   // keep the output monochrome
	noStatusBar bool   // the host draws no status bar, so the pane gets that row
}

// paneHost is what a host tells the Model it embeds as its diff pane.
type paneHost struct {
	// embedded marks the model as someone else's pane: the overlay manager is
	// shared with the host, which composes popups over the whole window, so
	// View must not compose them a second time.
	embedded bool
	// unfocused draws the lone diff pane with the inactive border, while the
	// host's side pane has the focus. Review mode never sets it, a lone pane is
	// always the focused one there.
	unfocused bool
	// annotationsHidden suppresses annotation rows and markers, for diffs whose
	// line numbers are not worktree line numbers (the staged side, commit and
	// stash diffs), where review annotations would land on the wrong lines.
	annotationsHidden bool
	// lineSelected reports whether a diff row belongs to a selection the host
	// owns (commit-mode line staging). nil = none. The result is folded into
	// lineRenderFlags so cached rows repaint correctly.
	lineSelected func(idx int) bool
}

// embedAsPane marks the model as a host's diff pane. Called once, at
// construction, before the model is driven.
func (m *Model) embedAsPane() { m.host.embedded = true }

// setPaneUnfocused draws the pane with the inactive border while the host's own
// pane holds the focus.
func (m *Model) setPaneUnfocused(unfocused bool) { m.host.unfocused = unfocused }

// setPaneAnnotationsHidden turns annotation rows and markers off for a diff
// whose line numbers do not refer to the working tree.
func (m *Model) setPaneAnnotationsHidden(hidden bool) { m.host.annotationsHidden = hidden }

// setPaneLineSelected installs the host's row-selection predicate, nil to clear it.
func (m *Model) setPaneLineSelected(selected func(idx int) bool) { m.host.lineSelected = selected }

// hostSelected reports whether the host has selected the row.
func (m Model) hostSelected(idx int) bool {
	return m.host.lineSelected != nil && m.host.lineSelected(idx)
}

// --- what a host reads from and writes to the pane ---
//
// These are plain accessors over the pane's own state. They exist so the host
// names what it wants rather than reaching into the pane's grouped state, and
// so the write side goes through one place that keeps the cursor in range and
// the viewport in step with it.

// paneLines returns the diff rows currently loaded in the pane.
func (m Model) paneLines() []git.DiffLine { return m.file.lines }

// paneRowCount returns how many diff rows the pane holds.
func (m Model) paneRowCount() int { return len(m.file.lines) }

// paneCursor returns the row the diff cursor sits on.
func (m Model) paneCursor() int { return m.nav.diffCursor }

// setPaneCursor moves the diff cursor to row, clamped to the loaded rows, and
// scrolls the viewport to follow it. An empty pane ignores the call.
func (m *Model) setPaneCursor(row int) {
	n := len(m.file.lines)
	if n == 0 {
		return
	}
	m.nav.diffCursor = max(min(row, n-1), 0)
	m.syncViewportToCursor()
}

// setPaneHunkJump arms the cross-file hunk jump the pane applies when the next
// file finishes loading, nil to disarm it.
func (m *Model) setPaneHunkJump(forward *bool) { m.nav.pendingHunkJump = forward }

// paneSearching reports whether the pane is taking search input.
func (m Model) paneSearching() bool { return m.search.active }

// paneSearchMatches returns the rows the active search matched.
func (m Model) paneSearchMatches() []int { return m.search.matches }

// nextPaneLoad bumps the pane's load generation and returns it, so the host can
// stamp the fileLoadedMsg it feeds in and stale loads are dropped as usual.
func (m *Model) nextPaneLoad() uint64 {
	m.file.loadSeq++
	return m.file.loadSeq
}

// setPaneHighlighted replaces the syntax-highlighted rows of the loaded file.
func (m *Model) setPaneHighlighted(highlighted []string) { m.file.highlighted = highlighted }

// paneToggle names one of the view toggles the pane owns, so a host can read
// its state for the status bar without knowing where the pane keeps it.
type paneToggle int

const (
	toggleWrap paneToggle = iota
	toggleLineNumbers
	toggleWordDiff
	toggleBlame
	toggleCompact
	toggleCollapsed
)

// paneToggleOn reports whether the view toggle is currently on.
func (m Model) paneToggleOn(t paneToggle) bool {
	switch t {
	case toggleWrap:
		return m.modes.wrap
	case toggleLineNumbers:
		return m.modes.lineNumbers
	case toggleWordDiff:
		return m.modes.wordDiff
	case toggleBlame:
		return m.modes.showBlame
	case toggleCompact:
		return m.modes.compact
	case toggleCollapsed:
		return m.modes.collapsed.enabled
	}
	return false
}

// focusPaneDiff puts the pane's own focus on the diff, where a host that has no
// tree of its own keeps it.
func (m *Model) focusPaneDiff() { m.layout.focus = paneDiff }
