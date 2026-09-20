package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
)

// wheelStep is the number of lines one wheel notch scrolls by. Shift+wheel
// uses half the viewport height instead. Shared with overlay.WheelStep so
// overlay popup scroll feels the same as diff-pane scroll.
const wheelStep = overlay.WheelStep

// wheelRenderDelay is the idle window after the last diff-pane wheel event
// before the deferred cursor pin + SetContent(renderDiff()) runs. Each wheel
// event updates the viewport YOffset synchronously and defers both the cursor
// pin and the diff render to a single tea.Tick so a burst of wheel events
// produces one pin + render at burst-end instead of per-event work. Short
// enough that the cursor highlight reappears quickly after the user stops
// scrolling, long enough to coalesce a typical trackpad flick into one render.
const wheelRenderDelay = 30 * time.Millisecond

// wheelState coalesces diff-pane wheel events: one pin and render per burst
// instead of one per event.
type wheelState struct {
	gen           int
	renderPending bool
	tickInFlight  bool
}

// wheelDebounceMsg is the deferred flush trigger for diff-pane wheel events.
// gen captures the wheel generation at scheduling time so handleWheelDebounce
// can distinguish a settled burst (gen matches -> flush) from an in-progress
// burst (gen advanced -> reschedule a fresh tick for the current gen).
type wheelDebounceMsg struct {
	gen int
}

// hitZone identifies which interactive area a mouse event targets.
type hitZone int

const (
	hitNone   hitZone = iota // outside any interactive area (borders, gaps, out-of-bounds)
	hitTree                  // tree pane
	hitDiff                  // diff pane body (below the diff header)
	hitStatus                // status bar row(s)
	hitHeader                // diff header row (file path) - currently a no-op zone
)

// statusBarHeight returns the number of rows occupied by the status bar.
// 0 when the status bar is hidden, otherwise 1.
func (m Model) statusBarHeight() int {
	if m.cfg.noStatusBar {
		return 0
	}
	return 1
}

// diffTopRow returns the first screen row (0-based y) of diff viewport content.
// accounts for the pane top border (row 0) and the diff header row (row 1),
// so the viewport always starts at row 2 regardless of whether the tree pane
// is visible.
func (m Model) diffTopRow() int {
	return 2 + m.noteRows()
}

// treeTopRow returns the first screen row (0-based y) of tree pane content.
// accounts for the pane top border only - unlike diff, the tree pane has no
// internal header row, so content starts at row 1.
func (m Model) treeTopRow() int {
	return 1
}

// hitTest classifies a screen coordinate into a hitZone for mouse-event routing.
// the classification is pure arithmetic over m.layout state and does not
// inspect any dynamic UI content. ordering matters: status bar is checked
// first (y at bottom), then x is used to split tree vs diff columns, and
// finally y is used within each column to reject the diff header row or tree
// top border.
func (m Model) hitTest(x, y int) hitZone {
	if x < 0 || y < 0 || x >= m.layout.width || y >= m.layout.height {
		return hitNone
	}
	if sbh := m.statusBarHeight(); sbh > 0 && y >= m.layout.height-sbh {
		return hitStatus
	}
	// pane bottom border row sits just above the status bar (or the last
	// row when the status bar is hidden). clicks on the border must not
	// map into the viewport - without this guard, clickDiff would compute
	// a row one past the visible content.
	if y == m.layout.height-m.statusBarHeight()-1 {
		return hitNone
	}

	// tree block spans columns [0, treeWidth+1] when visible: left border +
	// treeWidth content columns + right border = treeWidth+2 columns total.
	// diff block picks up at column treeWidth+2.
	if !m.treePaneHidden() && x < m.layout.treeWidth+2 {
		if y < m.treeTopRow() {
			return hitNone
		}
		return hitTree
	}

	if y == 0 {
		return hitNone // diff pane top border - mirror of treeTopRow() guard above
	}
	if y < m.diffTopRow() {
		return hitHeader
	}
	return hitDiff
}

// handleMouse routes a tea.MouseMsg through the modal-state checks and into
// per-button dispatch. mouse events are only generated when
// tea.WithMouseCellMotion is enabled (i.e. --no-mouse is off), so this
// handler never runs in the opted-out path. wheel routing is by pointer
// position, not by current focus - this matches terminal conventions where
// scrolling follows the cursor.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// swallow during modal states - input belongs to the modal, not the
	// viewport beneath. hints are preserved here so the modal prompt (e.g.
	// reload's "press y to confirm") stays visible while the event is
	// discarded. in the keyboard path the prompt is also replaced by a new
	// hint from handlePendingReload, but mouse events don't transition the
	// modal, so dropping the hint would leave an invisible modal.
	if m.inConfirmDiscard || m.reload.pending || m.annot.annotating || m.search.active || m.output.saving {
		return m, nil
	}
	if m.overlay.Active() {
		return m.handleOverlayMouse(msg)
	}

	// reload, output, compact-mode, and editor hints persist for exactly one
	// render cycle. any mouse event that reaches this point dismisses them,
	// mirroring handleKey.
	m.reload.hint = ""
	m.output.hint = ""
	m.compact.hint = ""
	m.editorState.hint = ""

	zone := m.hitTest(msg.X, msg.Y)

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if msg.Action != tea.MouseActionPress {
			return m, nil // guard against non-press wheel emissions for symmetry with left-click
		}
		return m.handleWheel(zone, -m.wheelStepFor(msg.Shift))
	case tea.MouseButtonWheelDown:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.handleWheel(zone, m.wheelStepFor(msg.Shift))
	case tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight:
		// horizontal wheel is intentionally swallowed - horizontal scroll
		// stays keyboard-driven so users keep a single mental model.
		return m, nil
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil // ignore release and motion while holding
		}
		switch zone {
		case hitTree:
			return m.clickTree(msg.Y)
		case hitDiff:
			return m.clickDiff(msg.Y)
		case hitNone, hitStatus, hitHeader:
			return m, nil
		}
		return m, nil
	default:
		// right, middle, back, forward, none - no-op for this pass.
		return m, nil
	}
}

// handleOverlayMouse routes a mouse event to the active overlay. wheel events
// drive the overlay's own scroll/cursor navigation. clicks and other buttons
// are consumed so they don't leak through to the panes underneath. outcomes
// that need model-side side effects (annotation/file jump, theme preview/confirm)
// are dispatched through the same helpers as the keyboard path. Canceled and
// Closed branches mirror the keyboard dispatch for symmetry but the current
// overlay mouse handlers never emit them - a mouse click either confirms
// (themeselect) or is a no-op.
func (m Model) handleOverlayMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	out := m.overlay.HandleMouse(msg)
	switch out.Kind {
	case overlay.OutcomeAnnotationChosen:
		return m.jumpToAnnotationTarget(out.AnnotationTarget)
	case overlay.OutcomeThemePreview:
		m.previewThemeByName(out.ThemeChoice.Name)
	case overlay.OutcomeThemeConfirmed:
		m.confirmThemeByName(out.ThemeChoice.Name)
	case overlay.OutcomeThemeCanceled:
		m.cancelThemeSelect()
	case overlay.OutcomeFileChosen:
		return m.jumpToFile(out.FileChoice.Path)
	case overlay.OutcomeClosed, overlay.OutcomeNone, overlay.OutcomeConfirmed, overlay.OutcomeMenuChosen,
		overlay.OutcomePromptSubmitted, overlay.OutcomePromptEditor:
	}
	return m, nil
}

// wheelStepFor returns the wheel scroll step for the diff pane. Plain wheel
// scrolls by the wheelStep constant. Shift+wheel scrolls by half the
// viewport height to match the keyboard half-page shortcut. The tree
// path in handleWheel ignores the magnitude and uses single-step cursor
// navigation regardless, so this only governs the diff-pane delta.
func (m Model) wheelStepFor(shift bool) int {
	if !shift {
		return wheelStep
	}
	return max(1, m.layout.viewport.Height/2)
}

// handleWheel routes a vertical wheel event to the pane under the pointer,
// not to the focused pane. delta is positive for wheel-down. In the diff pane
// only the viewport moves: the cursor keeps its line and is pinned to the
// first or last visible line when it would scroll out of view. The pin and
// the re-render are deferred by a tea.Tick debounce (wheelRenderDelay), so a
// burst of events costs one SetYOffset each and a single render at its end.
func (m Model) handleWheel(zone hitZone, delta int) (tea.Model, tea.Cmd) {
	switch zone {
	case hitDiff:
		if !m.scrollDiffViewportBy(delta) {
			return m, nil
		}
		m.wheel.renderPending = true
		m.wheel.gen++
		gen := m.wheel.gen

		// schedule a tick only when none is in flight. subsequent wheels in the
		// same burst just bump gen. an in-flight tick fires at its own
		// wallclock deadline and reschedules itself if it lands on a stale gen
		// (see handleWheelDebounce). this keeps message count proportional to
		// burst duration, not wheel event count - without this guard each
		// wheel event spawns a tea.Tick goroutine and a debounce Msg, every
		// one of which forces another Update + View cycle (~2ms each).
		if m.wheel.tickInFlight {
			return m, nil
		}
		m.wheel.tickInFlight = true
		return m, tea.Tick(wheelRenderDelay, func(time.Time) tea.Msg {
			return wheelDebounceMsg{gen: gen}
		})
	case hitTree:
		// tree wheel = direct cursor navigation, one entry per notch.
		// no debounce, no shift-half-page tricks (those are diff-pane things).
		// the tree is small and cheap so single-step matches j/k semantics.
		motion := sidepane.MotionDown
		if delta < 0 {
			motion = sidepane.MotionUp
		}
		m.tree.Move(motion)
		m.pendingAnnotJump = nil
		m.nav.pendingHunkJump = nil
		return m.loadSelectedIfChanged()
	case hitNone, hitStatus, hitHeader:
		// no-op zones - wheel outside the interactive panes is ignored.
	}
	return m, nil
}

// handleWheelDebounce handles the deferred tick of a wheel burst: nothing to
// do when another path already flushed, a fresh tick when the burst has moved
// on since this one was scheduled, otherwise the flush itself.
func (m Model) handleWheelDebounce(msg wheelDebounceMsg) (tea.Model, tea.Cmd) {
	// renderPending cleared by some other path (handleKey flushed, resize
	// flushed, etc.): the in-flight tick is done, no more rescheduling.
	if !m.wheel.renderPending {
		m.wheel.tickInFlight = false
		return m, nil
	}
	// gen has advanced past msg.gen: the burst is still going. reschedule a
	// new tick for the current gen and stay tickInFlight=true. without this
	// reschedule the burst would never flush after the original tick fires
	// stale.
	if msg.gen != m.wheel.gen {
		curGen := m.wheel.gen
		return m, tea.Tick(wheelRenderDelay, func(time.Time) tea.Msg {
			return wheelDebounceMsg{gen: curGen}
		})
	}
	// gen matches: burst has been idle for wheelRenderDelay. flushWheelPending
	// clears both renderPending and tickInFlight, so the next burst's first
	// wheel reschedules a fresh tick.
	m.flushWheelPending()
	return m, nil
}

// clickTree handles a left-click press in the tree pane. the click
// both focuses the pane and selects the entry under the pointer - same as
// pressing j/k to land on the entry. when the entry is a file, the diff
// load is triggered via loadSelectedIfChanged. on a directory row or an
// out-of-range row the click just moves the cursor with no load (mirrors
// j-landing semantics).
func (m Model) clickTree(y int) (tea.Model, tea.Cmd) {
	row := y - m.treeTopRow()
	m.layout.focus = paneTree
	if !m.tree.SelectByVisibleRow(row) {
		return m, nil
	}
	m.pendingAnnotJump = nil
	m.nav.pendingHunkJump = nil
	return m.loadSelectedIfChanged()
}

// scrollDiffViewportBy shifts the diff viewport by delta rows, positive for
// down, and reports whether the offset changed, which means the caller owes a
// deferred cursor pin and render. The cursor stays on its old line until that
// deferred work runs at the end of the burst or an earlier flush.
func (m *Model) scrollDiffViewportBy(delta int) bool {
	if m.file.name == "" {
		return false
	}
	maxOffset := max(0, m.layout.viewport.TotalLineCount()-m.layout.viewport.Height)
	current := m.layout.viewport.YOffset
	target := max(0, min(current+delta, maxOffset))
	if target == current {
		return false
	}
	m.layout.viewport.SetYOffset(target)
	return true
}

// flushWheelPending applies the cursor pin and diff re-render a wheel burst
// left pending, and clears both wheel flags. Call it before anything that
// reads the diff cursor or the rendered content. No-op without a pending
// render, and the re-render is skipped when the cursor needed no pin.
func (m *Model) flushWheelPending() {
	if !m.wheel.renderPending {
		return
	}
	if m.pinDiffCursorTo(m.layout.viewport.YOffset) {
		m.layout.viewport.SetContent(m.renderDiff())
	}
	m.wheel.renderPending = false
	m.wheel.tickInFlight = false
}

// pinDiffCursorTo moves the diff cursor into view when newOffset would leave
// it off-screen, to the topmost or bottommost visible row. It reports whether
// the cursor moved, so the caller knows a re-render is needed. Visibility is
// judged by the marker row (the line's first visual row), and a line
// straddling the viewport top hands the cursor to the next line whose marker
// is inside the viewport.
func (m *Model) pinDiffCursorTo(newOffset int) bool {
	if len(m.file.lines) == 0 {
		return false
	}
	cursorTop, cursorBottom := m.cursorVisualRange()
	viewTop := newOffset
	viewBottom := newOffset + m.layout.viewport.Height - 1
	if cursorTop >= viewTop && cursorTop <= viewBottom {
		return false // cursor marker already visible
	}
	targetRow := viewBottom
	if cursorTop < viewTop {
		// cursor is above the viewport. in wrap mode the cursor line may have
		// continuation rows visible at viewTop - advance past them.
		if cursorBottom >= viewTop {
			targetRow = min(cursorBottom+1, viewBottom)
		} else {
			targetRow = viewTop
		}
	}
	idx, onAnnot := m.visualRowToDiffLine(targetRow)
	if idx == m.nav.diffCursor && onAnnot == m.nav.onAnnotationRow {
		return false
	}
	m.nav.diffCursor = idx
	m.nav.onAnnotationRow = onAnnot
	return true
}

// clickDiff handles a left-click press in the diff viewport. the click
// focuses the diff pane and moves the diff cursor to the logical line
// under the pointer. when the click lands on an injected annotation
// sub-row, onAnnotationRow is set so subsequent navigation treats the
// cursor as being on the annotation rather than the diff line above it.
func (m Model) clickDiff(y int) (tea.Model, tea.Cmd) {
	if m.file.name == "" {
		return m, nil // no file loaded - nothing to focus or point at
	}
	row := (y - m.diffTopRow()) + m.layout.viewport.YOffset
	idx, onAnnot := m.visualRowToDiffLine(row)
	m.layout.focus = paneDiff
	m.nav.diffCursor = idx
	m.nav.onAnnotationRow = onAnnot
	m.syncViewportToCursor()
	return m, nil
}
