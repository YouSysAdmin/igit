package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// handleMouse routes mouse events: the diff column is handed to the embedded
// review model (wheel scrolling with its debounce, clicks placing the cursor)
// with X shifted past the side pane. the side pane maps the wheel to cursor
// moves and a left click to row selection, as in review mode.
func (c *CommitModel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if c.overlay.Active() {
		if c.overlay.Kind() == overlay.KindThemeSelect {
			// the theme selector belongs to the embedded review model. it composes
			// over the whole window, so the coordinates need no shift
			next, cmd := c.diff.handleOverlayMouse(msg)
			if m, ok := next.(Model); ok {
				c.diff = m
			}
			return cmd
		}
		c.overlay.HandleMouse(msg)
		return nil
	}
	if !c.ready {
		return nil
	}
	c.hint = ""
	sideW := c.sideWidth()
	if msg.X >= sideW {
		msg.X -= sideW
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && c.diff.paneRowCount() > 0 {
			c.focus = focusDiff
		}
		next, cmd := c.diff.handleMouse(msg)
		if m, ok := next.(Model); ok {
			c.diff = m
		}
		return cmd
	}
	if msg.Action != tea.MouseActionPress {
		return nil
	}
	switch msg.Button { //nolint:exhaustive // other buttons are ignored in the side pane
	case tea.MouseButtonWheelDown:
		return c.sideMove(keymap.ActionDown)
	case tea.MouseButtonWheelUp:
		return c.sideMove(keymap.ActionUp)
	case tea.MouseButtonLeft:
		return c.sideClick(msg.Y)
	}
	return nil
}

// sideMove moves the active side list one row in the given direction.
func (c *CommitModel) sideMove(dir keymap.Action) tea.Cmd {
	if c.tab != tabFiles || c.hist.detail != nil {
		return c.moveHistoryList(dir)
	}
	return c.moveList(dir)
}

// sideClick selects the side pane row under y: row 0 is the top border, then
// the tab strip and its spacer, so list rows start at y == 1+sideHeaderRows and
// are offset by the scroll position.
func (c *CommitModel) sideClick(y int) tea.Cmd {
	const listTop = 1 + sideHeaderRows
	l := c.activeList()
	row := y - listTop + l.ScrollState().Offset
	if y < listTop || !l.SelectIndex(row) {
		return nil
	}
	c.focus = focusSide
	c.clearSelection()
	l.EnsureVisible(max(1, c.sidePaneHeight()-sideHeaderRows))
	if c.tab != tabFiles || c.hist.detail != nil {
		return c.loadDiffForHistoryCursor()
	}
	return c.loadDiffForCursor()
}
