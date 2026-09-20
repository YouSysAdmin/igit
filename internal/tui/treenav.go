package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
)

// Navigation of review mode's file tree, and the parts of diff navigation that
// need it. The diff pane itself knows nothing about a tree: commit mode drives
// the same pane from its own file list.

// handleTreeAction dispatches a resolved action when the tree pane is focused.
func (m Model) handleTreeAction(action keymap.Action) (tea.Model, tea.Cmd) {
	// the scroll_diff_* actions scroll the diff pane while the tree keeps
	// focus. Handled first and returned early so the
	// tree-navigation tail (EnsureVisible, loadSelectedIfChanged) does not run -
	// the tree selection is unchanged.
	switch action {
	case keymap.ActionScrollDiffDown:
		m.scrollDiffViewportLine(wheelStep)
		return m, nil
	case keymap.ActionScrollDiffUp:
		m.scrollDiffViewportLine(-wheelStep)
		return m, nil
	case keymap.ActionScrollDiffPageDown:
		m.scrollDiffViewportLine(m.pageRows())
		return m, nil
	case keymap.ActionScrollDiffPageUp:
		m.scrollDiffViewportLine(-m.pageRows())
		return m, nil
	case keymap.ActionScrollDiffHalfPageDown:
		m.scrollDiffViewportLine(m.halfPageRows())
		return m, nil
	case keymap.ActionScrollDiffHalfPageUp:
		m.scrollDiffViewportLine(-m.halfPageRows())
		return m, nil
	default: // all other actions fall through to tree navigation below
	}

	switch action {
	case keymap.ActionDown:
		m.tree.Move(sidepane.MotionDown)
	case keymap.ActionUp:
		m.tree.Move(sidepane.MotionUp)
	case keymap.ActionPageDown:
		m.tree.Move(sidepane.MotionPageDown, m.treePageSize())
	case keymap.ActionHalfPageDown:
		m.tree.Move(sidepane.MotionPageDown, max(1, m.treePageSize()/2))
	case keymap.ActionPageUp:
		m.tree.Move(sidepane.MotionPageUp, m.treePageSize())
	case keymap.ActionHalfPageUp:
		m.tree.Move(sidepane.MotionPageUp, max(1, m.treePageSize()/2))
	case keymap.ActionHome:
		m.tree.Move(sidepane.MotionFirst)
	case keymap.ActionEnd:
		m.tree.Move(sidepane.MotionLast)
	case keymap.ActionFocusDiff, keymap.ActionScrollRight:
		if m.file.name != "" {
			m.layout.focus = paneDiff
		}
	default: // actions handled by handleKey (quit, toggle_pane, filter, etc.) - not repeated here
	}
	m.pendingAnnotJump = nil    // clear pending annotation jump on manual navigation
	m.nav.pendingHunkJump = nil // clear pending hunk jump on manual navigation
	m.tree.EnsureVisible(m.treePageSize())
	return m.loadSelectedIfChanged()
}

// handleSwitchToTree switches focus to the tree pane when there is one: not
// hidden and not a single-file review.
func (m Model) handleSwitchToTree() (tea.Model, tea.Cmd) {
	if !m.treePaneHidden() {
		m.layout.focus = paneTree
	}
	return m, nil
}

// crossFileHunkNav steps the file tree to the adjacent file and arms the jump
// that lands on its first (last) hunk once the diff is loaded. A no-op at the
// ends of the list.
func (m Model) crossFileHunkNav(forward bool) (tea.Model, tea.Cmd) {
	dir, jump := sidepane.DirectionNext, true
	if !forward {
		dir, jump = sidepane.DirectionPrev, false
	}
	if !m.tree.HasFile(dir) {
		return m, nil
	}
	m.nav.pendingHunkJump = &jump
	m.tree.StepFile(dir)
	return m.loadSelectedIfChanged()
}
