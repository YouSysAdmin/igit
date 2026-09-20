package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
	"github.com/yousysadmin/igit/internal/tui/style"
)

// resize records the window size and sizes the embedded diff pane to the
// right-hand column (the whole width while the side pane is hidden).
func (c *CommitModel) resize(width, height int) {
	c.width, c.height = width, height
	c.ready = true
	diffHeight := height
	if !c.noStatusBar {
		diffHeight--
	}
	// the diff model owns the borders of its (only) pane, so it is told a window
	// exactly as wide as the right column
	next, _ := c.diff.handleResize(tea.WindowSizeMsg{Width: max(10, width-c.sideWidth()), Height: max(3, diffHeight)})
	if m, ok := next.(Model); ok {
		c.diff = m
	}
}

// sideWidth is the side pane width including its borders. 0 while hidden.
func (c *CommitModel) sideWidth() int {
	if c.sideHidden {
		return 0
	}
	return max(minTreeWidth, c.width*c.sideWidthRatio/10)
}

// toggleSidePane hides or shows the side pane, as toggle_tree does with the
// file tree in review mode. Hiding moves the focus to the diff. showing gives
// it back to the list.
func (c *CommitModel) toggleSidePane() {
	c.sideHidden = !c.sideHidden
	if c.sideHidden {
		c.focus = focusDiff
	} else {
		c.focus = focusSide
		c.clearSelection()
	}
	if c.ready {
		c.resize(c.width, c.height)
	}
}

// showSidePane makes a hidden side pane visible again (focus_tree, esc).
func (c *CommitModel) showSidePane() {
	if c.sideHidden {
		c.toggleSidePane()
	}
}

// sidePaneHeight is the inner height of the side pane (rows available to the list).
func (c *CommitModel) sidePaneHeight() int {
	h := c.height - 2 // borders
	if !c.noStatusBar {
		h--
	}
	return max(1, h)
}

// View renders the side pane, the diff pane, the status bar and any overlay.
func (c *CommitModel) View() string {
	if !c.ready {
		return "loading..."
	}
	res := c.diff.resolver
	c.diff.setPaneUnfocused(c.focus == focusSide && !c.sideHidden)
	main := c.diff.View()
	if !c.sideHidden {
		main = lipgloss.JoinHorizontal(lipgloss.Top, c.sidePane(), main)
	}
	main = c.overlay.Compose(main, overlay.RenderCtx{Width: c.width, Height: c.height, Resolver: res})
	if c.noStatusBar {
		return main
	}
	status := res.Style(style.StyleKeyStatusBar).Width(c.width).Render(c.statusBarText())
	return lipgloss.JoinVertical(lipgloss.Left, main, status)
}

// sidePane renders the bordered side column: tab strip, spacer and the active list.
func (c *CommitModel) sidePane() string {
	ph := c.sidePaneHeight()
	sideW := c.sideWidth()
	res := c.diff.resolver

	sideStyle := res.Style(style.StyleKeyTreePane)
	if c.focus == focusSide {
		sideStyle = res.Style(style.StyleKeyTreePaneActive)
	}
	inner := max(sideW-2, 1) // the border takes one column on each side
	header := c.tabsHeader(sideW)
	listContent := c.activeList().Render(sidepane.ListRender{
		Width: inner, Height: max(1, ph-sideHeaderRows), Focused: c.focus == focusSide,
		Empty: c.emptyListText(), Resolver: res,
	})
	sideContent := c.diff.padContentBg(lipgloss.JoinVertical(lipgloss.Left, header, "", listContent), inner, res.Color(style.ColorKeyTreePaneBg))
	return sideStyle.Width(inner).Height(ph).Render(sideContent)
}

// sideHeaderRows is what the tab strip and the blank row under it occupy.
const sideHeaderRows = 2

// tabsHeader renders the tab strip in the mode bar's style (selected tab
// highlighted, plain ASCII). a drilled-in entry shows its title instead. The
// strip shrinks with the pane: padded labels, then bare labels, then only the
// active tab between < > markers, so it never wraps onto a second row.
func (c *CommitModel) tabsHeader(width int) string {
	res := c.diff.resolver
	if d := c.hist.detail; d != nil {
		return res.Style(style.StyleKeyDirEntry).Render(" " + sidepane.TruncateRight(d.title, max(width-4, 4)))
	}
	inner := max(width-2, 1) // the borders take one column each
	for _, pad := range []string{" ", ""} {
		if strip := c.tabStrip(pad); lipgloss.Width(strip) <= inner {
			return strip
		}
	}
	label := sidepane.TruncateRight(c.tab.String(), max(inner-5, 1))
	return " " + res.Style(style.StyleKeyFileEntry).Render("<") + res.Style(style.StyleKeyFileSelected).Render(" "+label+" ") + res.Style(style.StyleKeyFileEntry).Render(">")
}

// tabStrip renders every tab label with the given inner padding.
func (c *CommitModel) tabStrip(pad string) string {
	res := c.diff.resolver
	parts := make([]string, 0, int(tabCount))
	for t := range tabCount {
		label := pad + t.String() + pad
		if t == c.tab {
			parts = append(parts, res.Style(style.StyleKeyFileSelected).Render(label))
		} else {
			parts = append(parts, res.Style(style.StyleKeyFileEntry).Render(label))
		}
	}
	return " " + strings.Join(parts, " ")
}

func (c *CommitModel) emptyListText() string {
	switch {
	case c.hist.detail != nil:
		return "  no files"
	case c.tab == tabBranches:
		return "  no branches"
	case c.tab == tabLog:
		return "  no commits"
	case c.tab == tabStash:
		return "  no stashes"
	case !c.statusLoaded:
		return "  loading status..."
	case c.filter != nil && len(c.status.Files) > 0:
		return "  no changes in the file scope"
	}
	return "  working tree clean"
}

// legend renders the key hints for the focused pane within width columns.
func (c *CommitModel) legend(width int, items ...legendItem) string {
	return renderLegend(c.keymap, width, items...)
}

// statusModeIcons returns the commit-mode view-toggle strip.
func (c *CommitModel) statusModeIcons() string {
	return renderStatusIcons(c.diff.resolver, c.statusIndicators())
}

// statusIndicators lists the commit-mode view toggles and their state. Only the
// toggles this mode owns are listed: a muted icon means the mode is off, so a
// lamp for a key that does nothing here would read as a lie.
func (c *CommitModel) statusIndicators() []statusIndicator {
	return []statusIndicator{
		{keymap.ActionToggleWrap, c.diff.paneToggleOn(toggleWrap)},
		{keymap.ActionSearch, len(c.diff.paneSearchMatches()) > 0},
		{keymap.ActionToggleTree, c.sideHidden},
		{keymap.ActionToggleLineNums, c.diff.paneToggleOn(toggleLineNumbers)},
		{keymap.ActionToggleWordDiff, c.diff.paneToggleOn(toggleWordDiff)},
	}
}

// statusBarText shows a transient hint, otherwise the branch summary and a
// context-sensitive key legend on the left, and the view-toggle strip plus the
// help key on the right, where review mode keeps them too.
func (c *CommitModel) statusBarText() string {
	if c.hint != "" {
		return c.hint
	}
	if c.diff.paneSearching() {
		return c.diff.searchBarText()
	}
	left := c.headSummary()
	items := c.sideLegend()
	if c.focus == focusDiff {
		left += c.diffStatusSuffix()
		items = c.diffLegend()
	}
	sep := c.diff.renderer.StatusBarSeparator()
	right := c.statusModeIcons()
	if help := statusHelpHint(c.keymap); help != "" {
		right += sep + help
	}
	// the status bar has one column of padding on each side, the legend fills
	// what the summary and the right block leave
	budget := c.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	legend := c.legend(budget, items...)
	if legend != "" {
		left += "  " + legend
	}
	return joinStatusSections(c.width, sidepane.TruncateRight(left, max(c.width-lipgloss.Width(right)-4, 1)), right, sep)
}

// sideLegend lists the key legend for the focused side pane, per tab.
func (c *CommitModel) sideLegend() []legendItem {
	mode := legendItem{action: keymap.ActionToggleMode, label: "review"}
	switch {
	case c.hist.detail != nil:
		return []legendItem{{action: keymap.ActionConfirm, label: "diff"}, {action: keymap.ActionDismiss, label: "back"}, mode}
	case c.tab == tabBranches:
		return []legendItem{
			{action: keymap.ActionConfirm, label: "checkout"}, {action: keymap.ActionCreate, label: "new"},
			{action: keymap.ActionDiscardChanges, label: "delete"}, {action: keymap.ActionRename, label: "rename"},
			{action: keymap.ActionMerge, label: "merge"}, {action: keymap.ActionRebase, label: "rebase"},
			{action: keymap.ActionReset, label: "reset"}, {action: keymap.ActionSetUpstream, label: "upstream"},
			{action: keymap.ActionPush, label: "push"}, {action: keymap.ActionPull, label: "pull"}, {action: keymap.ActionFetch, label: "fetch"},
		}
	case c.tab == tabLog:
		return []legendItem{{action: keymap.ActionConfirm, label: "files"}, {action: keymap.ActionTabFiles, label: "files tab"}}
	case c.tab == tabStash:
		return []legendItem{
			{action: keymap.ActionConfirm, label: "files"}, {action: keymap.ActionStash, label: "stash menu"},
			{action: keymap.ActionDiscardChanges, label: "drop"}, {action: keymap.ActionCreate, label: "new stash"},
		}
	}
	if fr, ok := c.cursorFile(); ok && fr.file.Conflict {
		// a conflict is settled, not staged, and the editor is the way to do it
		// by hand. Both are worth naming where the stage hints would be
		return []legendItem{
			{action: keymap.ActionStageToggle, label: "resolve"}, {action: keymap.ActionOpenFileInEditor, label: "edit"},
			{action: keymap.ActionDiscardChanges, label: "reset"}, {action: keymap.ActionAbortOrContinue, label: "abort/continue"},
			{action: keymap.ActionTogglePane, label: "diff"}, mode,
		}
	}
	return []legendItem{
		{action: keymap.ActionStageToggle, label: "stage"}, {action: keymap.ActionStageAll, label: "all"},
		{action: keymap.ActionUnstageAll, label: "unstage all"}, {action: keymap.ActionDiscardChanges, label: "discard"},
		{action: keymap.ActionOpenFileInEditor, label: "edit"}, {action: keymap.ActionTogglePane, label: "diff"}, mode,
	}
}

// diffLegend lists the key legend while the diff pane is focused.
func (c *CommitModel) diffLegend() []legendItem {
	if c.conflictedPath(c.staging.spec.Path) {
		// the diff of a conflict is settled block by block, not line by line
		return []legendItem{
			{action: keymap.ActionStageToggle, label: "resolve this conflict"}, {action: keymap.ActionOpenFileInEditor, label: "edit"},
			{action: keymap.ActionNextHunk, label: "next"}, {action: keymap.ActionTogglePane, label: "back"},
		}
	}
	items := []legendItem{{action: keymap.ActionStageToggle, label: "stage line"}, {action: keymap.ActionStageHunk, label: "stage hunk"}, {action: keymap.ActionVisualRange, label: "range"}}
	if c.staging.spec.Cached {
		items[0].label, items[1].label = "unstage line", "unstage hunk"
	} else {
		items = append(items, legendItem{action: keymap.ActionDiscardChanges, label: "discard"})
	}
	return append(items, legendItem{action: keymap.ActionToggleStagedView, label: "staged/unstaged"}, legendItem{action: keymap.ActionSearch, label: "search"}, legendItem{action: keymap.ActionTogglePane, label: "back"})
}

// diffStatusSuffix names the diff side and the active selection.
func (c *CommitModel) diffStatusSuffix() string {
	var b strings.Builder
	if c.staging.spec.Path != "" {
		if c.staging.spec.Cached {
			b.WriteString("  staged")
		} else {
			b.WriteString("  unstaged")
		}
	}
	switch c.staging.mode {
	case selectRange:
		first, last := c.selectedRows()
		fmt.Fprintf(&b, "  range %d lines", last-first+1)
	case selectHunk:
		b.WriteString("  hunk")
	case selectLine:
	}
	return b.String()
}

// headSummary formats the branch header: name, ahead/behind, stash count.
func (c *CommitModel) headSummary() string {
	h := c.status.Head
	var b strings.Builder
	switch {
	case !c.statusLoaded:
		b.WriteString("…")
	case h.Detached:
		b.WriteString("HEAD detached")
		if len(h.OID) >= 7 {
			b.WriteString(" at " + h.OID[:7])
		}
	case h.Unborn:
		b.WriteString(h.Name + " (no commits)")
	default:
		b.WriteString(h.Name)
	}
	if h.Ahead > 0 || h.Behind > 0 {
		fmt.Fprintf(&b, " ↑%d ↓%d", h.Ahead, h.Behind)
	}
	if h.Upstream != "" {
		b.WriteString(" · " + h.Upstream)
		if h.UpstreamGone {
			b.WriteString(" (gone)")
		}
	}
	if c.status.StashCount > 0 {
		fmt.Fprintf(&b, " · %d stash", c.status.StashCount)
	}
	if label := c.status.InProgress.Label(); label != "" {
		b.WriteString(" · " + label)
	}
	return b.String()
}
