package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/style"
)

// This file holds the status-bar parts both modes share. Each mode assembles
// its own bar (view.go for review, commit_view.go for commit) out of them, so
// spacing, the view-toggle strip and the key legend stay identical.

// joinStatusSections right-aligns right against width, keeping left where it is.
// Shared so both modes space their status bar the same way.
func joinStatusSections(width int, left, right, sep string) string {
	sepWidth := lipgloss.Width(sep)
	padding := width - lipgloss.Width(left) - lipgloss.Width(right) - 2 // 2 for status bar padding
	if left != "" && padding > sepWidth {
		return left + sep + strings.Repeat(" ", padding-sepWidth) + right
	}
	if padding > 0 {
		return left + strings.Repeat(" ", padding) + right
	}
	if left != "" {
		return left + sep + right
	}
	return right
}

// statusIconForAction is the single source of the status-bar glyphs: statusModeIcons renders
// them from here and the help overlay shows each beside the key that drives it. The reviewed
// slot is one glyph position with two actions behind it: the check glyph for marking, the circle for the unreviewed
// filter.
var statusIconForAction = map[keymap.Action]string{
	keymap.ActionToggleCollapsed:  "▼",
	keymap.ActionToggleCompact:    "⊂",
	keymap.ActionFilter:           "◉",
	keymap.ActionToggleWrap:       "↩",
	keymap.ActionSearch:           "≋",
	keymap.ActionToggleTree:       "⊟",
	keymap.ActionToggleLineNums:   "#",
	keymap.ActionToggleBlame:      "b",
	keymap.ActionToggleWordDiff:   "±",
	keymap.ActionMarkReviewed:     "✓",
	keymap.ActionFilterUnreviewed: "○",
	keymap.ActionToggleUntracked:  "∅",
}

// statusIndicator is one lamp of the view-toggle strip: the action it stands
// for and whether that mode is currently on.
type statusIndicator struct {
	action keymap.Action
	active bool
}

// renderStatusIcons paints the view-toggle strip both modes share. One icon per
// indicator, lit in the status foreground when the mode is on and muted when it
// is off. The same action draws the same icon in either mode, so the strip does
// not jump when the mode toggle switches panes.
func renderStatusIcons(res styleResolver, indicators []statusIndicator) string {
	mutedSeq := string(res.Color(style.ColorKeyMutedFg))
	activeSeq := string(res.Color(style.ColorKeyStatusFg))
	icons := make([]string, 0, len(indicators))
	for _, ind := range indicators {
		seq := mutedSeq
		if ind.active {
			seq = activeSeq
		}
		icons = append(icons, seq+statusIconForAction[ind.action])
	}
	return strings.Join(icons, " ") + activeSeq
}

// statusHelpHint names the key that opens the help popup. Both status bars end
// with it, and the name comes from the keymap so a rebind is reflected.
func statusHelpHint(km *keymap.Keymap) string {
	var best string
	for _, k := range km.KeysFor(keymap.ActionHelp) {
		if best == "" || len(k) < len(best) {
			best = k
		}
	}
	if best == "" {
		return ""
	}
	return keymap.DisplayKey(best) + " help"
}

// legendItem pairs an action with the short label the status bar shows for it.
// prefer names the binding to show when the action has several and the shortest
// one would mislead: confirm is bound to both "a" and "enter", and "a" reads as
// "annotate" next to the annotation hints. An unbound preference falls back to
// the shortest, so a rebind is still reflected.
type legendItem struct {
	action keymap.Action
	label  string
	prefer string
}

// renderLegend renders "[key] label" pairs from the active keymap, so remapped
// keys show up correctly. Unbound actions are left out. Nothing is half-drawn:
// an item that does not fit in width ends the legend, the first one included,
// so a narrow bar loses the hints rather than truncating one.
func renderLegend(km *keymap.Keymap, width int, items ...legendItem) string {
	var b strings.Builder
	for _, it := range items {
		k := legendKeyPreferring(km, it.action, it.prefer)
		if k == "" {
			continue
		}
		part := "[" + k + "] " + it.label
		if b.Len() > 0 {
			part = "  " + part
		}
		if lipgloss.Width(b.String())+lipgloss.Width(part) > width {
			break
		}
		b.WriteString(part)
	}
	return b.String()
}

// legendKey returns the display name of the shortest key bound to action,
// "" when the action is unbound.
func legendKey(km *keymap.Keymap, action keymap.Action) string {
	return legendKeyPreferring(km, action, "")
}

// legendKeyPreferring returns prefer's display name when it is still bound to
// action, otherwise the shortest binding.
func legendKeyPreferring(km *keymap.Keymap, action keymap.Action, prefer string) string {
	var best string
	if prefer != "" && slices.Contains(km.KeysFor(action), prefer) {
		return keymap.DisplayKey(prefer)
	}
	for _, k := range km.KeysFor(action) {
		if best == "" || len(k) < len(best) {
			best = k
		}
	}
	if best == "" {
		return ""
	}
	return keymap.DisplayKey(best)
}
