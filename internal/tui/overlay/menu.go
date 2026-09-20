package overlay

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/style"
)

const (
	menuMaxWidth  = 60
	menuMinWidth  = 24
	menuMargin    = 10
	menuBorderPad = 4
)

// MenuItem is one selectable entry: Key is the shortcut rune (0 for none),
// ID is echoed back in the outcome.
type MenuItem struct {
	Key   rune
	Label string
	ID    string
}

// MenuSpec describes a small vertical menu popup.
type MenuSpec struct {
	Title string
	Items []MenuItem
}

type menuOverlay struct {
	spec       MenuSpec
	cursor     int
	popupWidth int
}

func (o *menuOverlay) open(spec MenuSpec) {
	o.spec = spec
	o.cursor = 0
}

// handleKey picks an item by shortcut or enter, moves with up/down, and closes
// on esc or the dismiss action.
func (o *menuOverlay) handleKey(msg tea.KeyMsg, action keymap.Action) Outcome {
	if action == keymap.ActionDismiss || msg.Type == tea.KeyEsc {
		return Outcome{Kind: OutcomeClosed}
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && !msg.Alt {
		for _, it := range o.spec.Items {
			if it.Key != 0 && it.Key == msg.Runes[0] {
				return Outcome{Kind: OutcomeMenuChosen, MenuChoice: it.ID}
			}
		}
	}
	switch {
	case msg.Type == tea.KeyEnter:
		return o.chooseCurrent()
	case action == keymap.ActionDown || msg.Type == tea.KeyDown:
		o.cursor = min(o.cursor+1, len(o.spec.Items)-1)
	case action == keymap.ActionUp || msg.Type == tea.KeyUp:
		o.cursor = max(o.cursor-1, 0)
	}
	return Outcome{Kind: OutcomeNone}
}

func (o *menuOverlay) chooseCurrent() Outcome {
	if o.cursor < 0 || o.cursor >= len(o.spec.Items) {
		return Outcome{Kind: OutcomeNone}
	}
	return Outcome{Kind: OutcomeMenuChosen, MenuChoice: o.spec.Items[o.cursor].ID}
}

// handleMouse moves the cursor with the wheel. clicks are swallowed.
func (o *menuOverlay) handleMouse(msg tea.MouseMsg) Outcome {
	if msg.Action != tea.MouseActionPress {
		return Outcome{Kind: OutcomeNone}
	}
	switch msg.Button { //nolint:exhaustive // only the wheel moves the cursor
	case tea.MouseButtonWheelDown:
		o.cursor = min(o.cursor+1, max(len(o.spec.Items)-1, 0))
	case tea.MouseButtonWheelUp:
		o.cursor = max(o.cursor-1, 0)
	}
	return Outcome{Kind: OutcomeNone}
}

func (o *menuOverlay) render(ctx RenderCtx, mgr *Manager) string {
	o.popupWidth = max(min(ctx.Width-menuMargin, menuMaxWidth), menuMinWidth)
	width := o.popupWidth - menuBorderPad
	parts := make([]string, 0, len(o.spec.Items))
	for i, it := range o.spec.Items {
		key := "   "
		if it.Key != 0 {
			key = "[" + string(it.Key) + "]"
		}
		line := key + " " + it.Label
		if i == o.cursor {
			sel := ctx.Resolver.Style(style.StyleKeyFileSelected)
			parts = append(parts, sel.Width(width).Render(line))
			continue
		}
		parts = append(parts, string(ctx.Resolver.Color(style.ColorKeyNormalFg))+line+string(style.ResetFg))
	}
	box := ctx.Resolver.Style(style.StyleKeyInfoBox).Width(o.popupWidth).Render(strings.Join(parts, "\n"))
	title := o.spec.Title
	if title == "" {
		title = "menu"
	}
	return mgr.injectBorderTitle(box, " "+title+" ", borderEdgeText{
		popupWidth: o.popupWidth,
		accentFg:   string(ctx.Resolver.Color(style.ColorKeyAccentFg)),
		paneBg:     string(ctx.Resolver.Color(style.ColorKeyDiffPaneBg)),
	})
}
