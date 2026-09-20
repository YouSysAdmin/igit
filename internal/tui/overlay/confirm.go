package overlay

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/style"
)

const (
	confirmMaxWidth  = 70
	confirmMinWidth  = 30
	confirmMargin    = 10
	confirmBorderPad = 4
)

// ConfirmSpec describes a yes/no confirmation popup. ID is echoed back in the
// outcome so the caller knows which pending action was confirmed.
type ConfirmSpec struct {
	ID     string
	Title  string
	Body   string
	Yes    string // label for the confirming key, default "y"
	Danger bool   // render the body in the removal color
}

type confirmOverlay struct {
	spec       ConfirmSpec
	popupWidth int
}

func (c *confirmOverlay) open(spec ConfirmSpec) {
	c.spec = spec
}

// handleKey confirms on y/Y/enter, cancels on n, esc or the dismiss action.
func (c *confirmOverlay) handleKey(msg tea.KeyMsg, action keymap.Action) Outcome {
	if action == keymap.ActionDismiss || msg.Type == tea.KeyEsc {
		return Outcome{Kind: OutcomeClosed}
	}
	switch msg.Type { //nolint:exhaustive // only enter and runes matter, other keys are ignored
	case tea.KeyEnter:
		return Outcome{Kind: OutcomeConfirmed, ConfirmID: c.spec.ID}
	case tea.KeyRunes:
		if len(msg.Runes) != 1 {
			return Outcome{Kind: OutcomeNone}
		}
		switch msg.Runes[0] {
		case 'y', 'Y':
			return Outcome{Kind: OutcomeConfirmed, ConfirmID: c.spec.ID}
		case 'n', 'N', 'q':
			return Outcome{Kind: OutcomeClosed}
		}
	}
	return Outcome{Kind: OutcomeNone}
}

// handleMouse swallows clicks and wheel events so nothing leaks underneath.
func (c *confirmOverlay) handleMouse(_ tea.MouseMsg) Outcome {
	return Outcome{Kind: OutcomeNone}
}

func (c *confirmOverlay) render(ctx RenderCtx, mgr *Manager) string {
	c.popupWidth = max(min(ctx.Width-confirmMargin, confirmMaxWidth), confirmMinWidth)
	contentWidth := c.popupWidth - confirmBorderPad
	bodyColor := string(ctx.Resolver.Color(style.ColorKeyNormalFg))
	if c.spec.Danger {
		bodyColor = string(ctx.Resolver.Color(style.ColorKeyRemoveLineFg))
	}
	body := wrapLines(c.spec.Body, contentWidth)
	parts := make([]string, 0, len(body)+2)
	for _, line := range body {
		parts = append(parts, bodyColor+line+string(style.ResetFg))
	}
	yes := c.spec.Yes
	if yes == "" {
		yes = "y"
	}
	muted := string(ctx.Resolver.Color(style.ColorKeyMutedFg))
	parts = append(parts, "", muted+"["+yes+"] confirm   [n/esc] cancel"+string(style.ResetFg))
	box := ctx.Resolver.Style(style.StyleKeyInfoBox).Width(c.popupWidth).Render(strings.Join(parts, "\n"))
	title := c.spec.Title
	if title == "" {
		title = "confirm"
	}
	return mgr.injectBorderTitle(box, " "+title+" ", borderEdgeText{
		popupWidth: c.popupWidth,
		accentFg:   string(ctx.Resolver.Color(style.ColorKeyAccentFg)),
		paneBg:     string(ctx.Resolver.Color(style.ColorKeyDiffPaneBg)),
	})
}

// wrapLines breaks text into lines of at most width cells at word boundaries,
// keeping explicit newlines.
func wrapLines(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var out []string
	for para := range strings.SplitSeq(text, "\n") {
		line := ""
		for word := range strings.FieldsSeq(para) {
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}
