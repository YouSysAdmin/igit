package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/style"
)

func TestCommitModel_SidePaneToggle(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	assert.Contains(t, c.View(), "Files")
	fullWidth := c.diff.layout.width

	assert.Nil(t, c.Update(keyRunes("t")))
	assert.True(t, c.sideHidden)
	assert.Equal(t, 0, c.sideWidth())
	assert.Equal(t, focusDiff, c.focus)
	assert.NotContains(t, c.View(), "Branches")
	assert.Greater(t, c.diff.layout.width, fullWidth, "the diff pane takes the freed columns")

	// the list still moves from the diff with n/p while hidden
	cmd := c.Update(keyRunes("n"))
	require.NotNil(t, cmd)
	assert.Equal(t, 2, c.list.CursorIndex())

	// esc in the diff brings the pane back and focuses it
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyEsc}))
	assert.False(t, c.sideHidden)
	assert.Equal(t, focusSide, c.focus)
	assert.Equal(t, fullWidth, c.diff.layout.width)

	c.Update(keyRunes("t"))
	c.Update(keyRunes("h")) // focus_tree shows the hidden pane
	assert.False(t, c.sideHidden)
	assert.Equal(t, focusSide, c.focus)
}

func TestCommitModel_NoSidePaneStartsHidden(t *testing.T) {
	cfg := testCommitConfig()
	cfg.Repo = newRepoMock(sampleStatus())
	cfg.NoSidePane = true
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	c.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	assert.True(t, c.sideHidden)
	assert.Equal(t, focusDiff, c.focus)
	assert.Equal(t, 100, c.diff.layout.width)
}

func TestCommitModel_MouseIgnoresHiddenSidePane(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.Update(keyRunes("t"))
	before := c.list.CursorIndex()
	c.Update(wheel(2, 5, true)) // x=2 would be the side pane when shown, now it scrolls the diff
	assert.Equal(t, before, c.list.CursorIndex())
}

func TestCommitModel_LegendFollowsKeymap(t *testing.T) {
	cfg := testCommitConfig()
	cfg.Repo = newRepoMock(sampleStatus())
	km := keymap.DefaultCommit()
	km.Unbind(" ")
	km.Bind("x", keymap.ActionStageToggle)
	km.Unbind("?")
	cfg.Keymap = km
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	c.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	loadAll(t, c)

	legend := c.statusBarText()
	assert.Contains(t, legend, "[x] stage")
	assert.NotContains(t, legend, "help", "an unbound action leaves the legend")
	assert.Contains(t, legend, "[alt+g] review")

	// the first row is in the Staged section, so the diff shows the index side
	c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Contains(t, c.statusBarText(), "[x] unstage line")
	assert.Contains(t, c.statusBarText(), "[s] unstage hunk")
	assert.Contains(t, c.statusBarText(), "[I] staged/unstaged")
	assert.Contains(t, c.statusBarText(), "[tab] back")
	assert.NotContains(t, c.statusBarText(), "discard")
	runCmd(t, c, c.Update(keyRunes("I")))
	assert.Contains(t, c.statusBarText(), "[x] stage line")
	assert.Contains(t, c.statusBarText(), "[d] discard")
}

func TestCommitModel_LegendPerTab(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.Update(tea.WindowSizeMsg{Width: 200, Height: 40}) // wide enough for every legend item
	assert.Contains(t, c.statusBarText(), "[space] stage")
	c.Update(keyRunes("2"))
	assert.Contains(t, c.statusBarText(), "[enter] checkout")
	assert.Contains(t, c.statusBarText(), "[F] pull")
	c.Update(keyRunes("4"))
	assert.Contains(t, c.statusBarText(), "[S] stash menu")
}

func TestCommitModel_KeyPrefersShortestBinding(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	assert.Equal(t, "j", legendKey(c.keymap, keymap.ActionDown))
	assert.Equal(t, "space", legendKey(c.keymap, keymap.ActionStageToggle))
	assert.Empty(t, legendKey(c.keymap, keymap.ActionAnnotList))
}

func TestCommitModel_TabsHeaderFitsNarrowPane(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	c.tab = tabLog
	wide := c.tabsHeader(40)
	assert.Contains(t, wide, " Files ")
	assert.Contains(t, wide, " Stash ")
	assert.LessOrEqual(t, lipgloss.Width(wide), 38)

	bare := c.tabsHeader(28)
	assert.Contains(t, bare, "Files Branches Log Stash")
	assert.LessOrEqual(t, lipgloss.Width(bare), 26)

	narrow := c.tabsHeader(20)
	assert.Equal(t, " < Log >", narrow)
	assert.NotContains(t, narrow, "Files")

	tiny := c.tabsHeader(8)
	assert.LessOrEqual(t, lipgloss.Width(tiny), 8)
}

func TestCommitModel_TabTogglesPaneLikeReview(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	assert.Equal(t, focusSide, c.focus)
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyTab}))
	assert.Equal(t, focusDiff, c.focus)
	assert.True(t, c.staging.spec.Cached, "tab never flips the diff side")
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyTab}))
	assert.Equal(t, focusSide, c.focus)

	// tab from the diff also brings a hidden side pane back
	c.Update(keyRunes("t"))
	assert.Equal(t, focusDiff, c.focus)
	c.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.False(t, c.sideHidden)
	assert.Equal(t, focusSide, c.focus)
}

func TestCommitModel_SearchInDiff(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	cmd := c.Update(keyRunes("/"))
	assert.NotNil(t, cmd, "the text input blinks")
	assert.True(t, c.diff.paneSearching())
	assert.True(t, c.inputBusy())
	assert.Equal(t, focusDiff, c.focus)
	for _, r := range "TWO" {
		c.Update(keyRunes(string(r)))
	}
	assert.Equal(t, "/TWO", c.statusBarText())
	c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, c.diff.paneSearching())
	require.NotEmpty(t, c.diff.paneSearchMatches())
	assert.Equal(t, c.diff.paneSearchMatches()[0], c.diff.paneCursor())

	// n/N walk matches while a search is live instead of moving the file list
	before := c.list.CursorIndex()
	assert.Nil(t, c.Update(keyRunes("n")))
	assert.Equal(t, before, c.list.CursorIndex())

	// a new diff clears the search and n moves the list again
	runCmd(t, c, c.Update(keyRunes("I")))
	assert.Empty(t, c.diff.paneSearchMatches())
	runCmd(t, c, c.Update(keyRunes("n")))
	assert.Equal(t, before+1, c.list.CursorIndex())

	// with nothing loaded search is refused
	c.clearDiff()
	assert.Nil(t, c.Update(keyRunes("/")))
	assert.Contains(t, c.statusBarText(), "no diff to search")
}

func TestCommitModel_SidePaneWidthIncludesBorders(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.diff.file.lines = nil
	c.installRaw(gitops.DiffSpec{Path: "a.go"}, sampleRaw)
	for line := range strings.SplitSeq(c.View(), "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 120, "no row may exceed the window: %q", line)
	}
	assert.Equal(t, 120, c.sideWidth()+c.diff.layout.width)
}

func TestCommitModel_StatusBarNeverWraps(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.Update(tea.KeyMsg{Type: tea.KeyTab}) // the diff legend is the longest one
	for _, w := range []int{120, 80, 60, 40} {
		c.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		text := c.statusBarText()
		assert.LessOrEqual(t, lipgloss.Width(text), w-2, "width %d: %q", w, text)
		assert.Contains(t, text, "main", "the branch summary always stays")
	}
	c.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	assert.Contains(t, c.statusBarText(), "[tab] back", "everything fits in a wide window")
	c.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	assert.NotContains(t, c.statusBarText(), "[tab] back", "trailing items drop first")
	assert.Contains(t, c.statusBarText(), "[space] unstage line")
	c.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	assert.NotContains(t, c.statusBarText(), "[space]", "a legend item is dropped whole, never cut in half")
}

func TestCommitModel_StatusBarCarriesTheSameRightBlockAsReview(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	// the strip lists the view toggles commit mode owns, and only those: a
	// review-only lamp would stand for a key that does nothing here
	owned := []keymap.Action{
		keymap.ActionToggleWrap, keymap.ActionSearch, keymap.ActionToggleTree,
		keymap.ActionToggleLineNums, keymap.ActionToggleWordDiff,
	}
	indicators := c.statusIndicators()
	listed := make([]keymap.Action, 0, len(indicators))
	for _, ind := range indicators {
		listed = append(listed, ind.action)
	}
	assert.Equal(t, owned, listed)
	for _, a := range listed {
		assert.NotEmpty(t, statusIconForAction[a], "every listed toggle has an icon")
		assert.Contains(t, c.statusModeIcons(), statusIconForAction[a])
	}

	// the same action draws the same icon in review mode
	review := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	reviewIcons := map[keymap.Action]bool{}
	for _, ind := range review.statusIndicators() {
		reviewIcons[ind.action] = true
	}
	for _, a := range owned {
		assert.True(t, reviewIcons[a], "%s is a shared toggle, review mode lists it too", a)
	}

	text := c.statusBarText()
	assert.Contains(t, text, "? help", "the help key sits where review mode keeps it")
	assert.NotContains(t, text, "[?] help", "help is not repeated in the legend")
}

func TestCommitModel_StatusIndicatorFollowsItsToggle(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	state := func(a keymap.Action) bool {
		for _, ind := range c.statusIndicators() {
			if ind.action == a {
				return ind.active
			}
		}
		t.Fatalf("%s is not listed", a)
		return false
	}
	assert.False(t, state(keymap.ActionToggleWrap))
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	assert.True(t, state(keymap.ActionToggleWrap), "toggling wrap lights its lamp")

	assert.False(t, state(keymap.ActionToggleTree))
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	assert.True(t, state(keymap.ActionToggleTree), "hiding the side pane lights its lamp")
}

func TestCommitModel_OnlyFocusedPaneHasActiveBorder(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor) // tests run without a TTY, where colors would be stripped
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	cfg := testCommitConfig()
	cfg.Repo = newRepoMock(sampleStatus())
	res := style.NewResolver(style.Colors{Accent: "#ff0000", Border: "#00ff00"})
	cfg.DiffPane.StyleResolver = res
	cfg.DiffPane.StyleRenderer = style.NewRenderer(res)
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	c.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	loadAll(t, c)

	activeBorder := res.Style(style.StyleKeyTreePaneActive).GetBorderTopForeground()
	inactiveBorder := res.Style(style.StyleKeyTreePane).GetBorderTopForeground()
	require.NotEqual(t, activeBorder, inactiveBorder)
	countActive := func(view string) int {
		// the top border row of each pane starts with a corner. count corners painted in the active color
		seq := strings.TrimSuffix(lipgloss.NewStyle().Foreground(activeBorder).Render("┌"), "\x1b[0m")
		return strings.Count(view, seq)
	}
	assert.Equal(t, focusSide, c.focus)
	assert.Equal(t, 1, countActive(c.View()), "side pane focused: one active border")
	c.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, focusDiff, c.focus)
	assert.Equal(t, 1, countActive(c.View()), "diff focused: one active border")
	c.Update(keyRunes("t")) // hidden side pane: the lone diff pane is the focused one
	assert.Equal(t, 1, countActive(c.View()))
}
