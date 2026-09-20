package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/style"
)

// twoBlockRaw has two change blocks in one hunk: "-b/+B" and "-d/+D".
const twoBlockRaw = "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n@@ -1,5 +1,5 @@\n a\n-b\n+B\n c\n-d\n+D\n e\n"

// display rows: 0 divider, 1 a, 2 -b, 3 +B, 4 c, 5 -d, 6 +D, 7 e
// patch idx:    3          4    5     6     7    8     9     10

func newSelectCommit(t *testing.T) *CommitModel {
	t.Helper()
	repo := newRepoMock(gitops.Status{
		Head:  gitops.BranchHead{Name: "main", OID: "abc"},
		Files: []gitops.StatusEntry{{Path: "f.go", Index: gitops.Unmodified, Worktree: 'M'}},
	})
	repo.StagingDiffFunc = func(context.Context, gitops.DiffSpec) (string, error) { return twoBlockRaw, nil }
	c := newTestCommit(t, repo)
	loadAll(t, c)
	require.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	require.Equal(t, focusDiff, c.focus)
	return c
}

func repoOf(c *CommitModel) interface {
	ApplyLinesCalls() []struct {
		Ctx context.Context
		Req gitops.LineRequest
	}
} {
	return c.repo.(interface {
		ApplyLinesCalls() []struct {
			Ctx context.Context
			Req gitops.LineRequest
		}
	})
}

func TestCommitModel_StageSingleLine(t *testing.T) {
	c := newSelectCommit(t)
	assert.Equal(t, 1, c.diff.paneCursor(), "cursor skips the divider")
	c.Update(keyRunes("j")) // -b
	cmd := c.Update(keyRunes(" "))
	require.NotNil(t, cmd)
	cmd()
	calls := repoOf(c).ApplyLinesCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, gitops.LineRequest{Raw: twoBlockRaw, Path: "f.go", Indices: []int{5}, Op: gitops.StageLines}, calls[0].Req)
	assert.Equal(t, 2, c.staging.restoreRow)
}

func TestCommitModel_RangeSelection(t *testing.T) {
	c := newSelectCommit(t)
	c.Update(keyRunes("j")) // row 2
	c.Update(keyRunes("V"))
	assert.Equal(t, selectRange, c.staging.mode)
	c.Update(keyRunes("j"))
	c.Update(keyRunes("j")) // rows 2..4 selected (context row 4 included, ignored by transform)
	first, last := c.selectedRows()
	assert.Equal(t, 2, first)
	assert.Equal(t, 4, last)
	assert.True(t, c.isRowSelected(3))
	assert.False(t, c.isRowSelected(5))
	assert.Contains(t, c.statusBarText(), "range 3 lines")
	view := c.View()
	assert.Contains(t, view, "▎", "selected rows carry the selection glyph")

	cmd := c.Update(keyRunes(" "))
	require.NotNil(t, cmd)
	cmd()
	calls := repoOf(c).ApplyLinesCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, []int{5, 6}, calls[0].Req.Indices, "the context row in the range is not a change")
	assert.Equal(t, selectLine, c.staging.mode, "selection clears after applying")

	// extend keys start a range on their own. esc clears it without leaving the pane
	c.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	assert.Equal(t, selectRange, c.staging.mode)
	c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, selectLine, c.staging.mode)
	assert.Equal(t, focusDiff, c.focus)
	c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, focusSide, c.focus)
	assert.False(t, c.isRowSelected(2), "no selection painted while the side pane has focus")
}

func TestCommitModel_HunkSelection(t *testing.T) {
	c := newSelectCommit(t)
	c.Update(keyRunes("j"))
	c.Update(keyRunes("j")) // row 3 (+B)
	c.Update(keyRunes("v"))
	first, last := c.selectedRows()
	assert.Equal(t, 2, first)
	assert.Equal(t, 3, last)
	assert.Contains(t, c.statusBarText(), "hunk")
	// moving to the second block re-targets the selection
	c.Update(keyRunes("]"))
	first, last = c.selectedRows()
	assert.Equal(t, 5, first)
	assert.Equal(t, 6, last)
	cmd := c.Update(keyRunes(" "))
	cmd()
	assert.Equal(t, []int{8, 9}, repoOf(c).ApplyLinesCalls()[0].Req.Indices)
	// on a context row hunk mode falls back to the cursor line
	c.Update(keyRunes("v"))
	c.diff.nav.diffCursor = 4
	first, last = c.selectedRows()
	assert.Equal(t, 4, first)
	assert.Equal(t, 4, last)
	c.Update(keyRunes("v"))
	assert.Equal(t, selectLine, c.staging.mode, "second v toggles hunk mode off")
}

func TestCommitModel_UnstageLinesFromStagedView(t *testing.T) {
	c := newSelectCommit(t)
	c.installRaw(gitops.DiffSpec{Path: "f.go", Cached: true}, twoBlockRaw)
	c.Update(keyRunes("j"))
	cmd := c.Update(keyRunes(" "))
	cmd()
	assert.Equal(t, gitops.UnstageLines, repoOf(c).ApplyLinesCalls()[0].Req.Op)
	// discard is refused on the staged side
	c.Update(keyRunes("d"))
	assert.False(t, c.overlay.Active())
	assert.Contains(t, c.statusBarText(), "press I")
}

func TestCommitModel_DiscardLinesFlow(t *testing.T) {
	c := newSelectCommit(t)
	c.Update(keyRunes("j"))
	c.Update(keyRunes("V"))
	c.Update(keyRunes("j"))
	c.Update(keyRunes("d"))
	require.Equal(t, overlay.KindConfirm, c.overlay.Kind())
	assert.Contains(t, c.View(), "Discard 2 selected lines in f.go")
	c.Update(keyRunes("n"))
	assert.Empty(t, repoOf(c).ApplyLinesCalls())
	c.Update(keyRunes("d"))
	cmd := c.Update(keyRunes("y"))
	require.NotNil(t, cmd)
	cmd()
	calls := repoOf(c).ApplyLinesCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, gitops.DiscardLines, calls[0].Req.Op)
	assert.Equal(t, []int{5, 6}, calls[0].Req.Indices)

	// single line wording
	c.Update(keyRunes("d"))
	assert.Contains(t, c.View(), "Discard the selected change in f.go")
	c.Update(tea.KeyMsg{Type: tea.KeyEsc})
}

func TestCommitModel_LineOpGuards(t *testing.T) {
	c := newSelectCommit(t)
	// context row: nothing selected
	c.diff.nav.diffCursor = 1
	assert.Nil(t, c.Update(keyRunes(" ")))
	assert.Contains(t, c.statusBarText(), "select changed lines")
	c.Update(keyRunes("d"))
	assert.False(t, c.overlay.Active())

	// binary diff
	c.installRaw(gitops.DiffSpec{Path: "bin"}, "diff --git a/bin b/bin\nBinary files a/bin and b/bin differ\n")
	assert.Nil(t, c.Update(keyRunes(" ")))
	assert.Contains(t, c.statusBarText(), "binary file")

	// empty diff
	c.installRaw(gitops.DiffSpec{Path: "empty"}, "")
	assert.Nil(t, c.Update(keyRunes(" ")))
	assert.Contains(t, c.statusBarText(), "no diff loaded")
}

func TestCommitModel_CursorRestoredAfterReload(t *testing.T) {
	c := newSelectCommit(t)
	c.Update(keyRunes("j"))
	c.Update(keyRunes("j"))
	c.Update(keyRunes("j")) // row 4
	c.staging.restoreRow = 6
	c.installRaw(gitops.DiffSpec{Path: "f.go"}, twoBlockRaw)
	assert.Equal(t, 6, c.diff.paneCursor())
	assert.Equal(t, -1, c.staging.restoreRow)

	// same view refresh keeps the cursor. a shorter diff clamps it
	c.diff.nav.diffCursor = 7
	c.installRaw(gitops.DiffSpec{Path: "f.go"}, sampleRaw)
	assert.Equal(t, 4, c.diff.paneCursor())

	// switching files resets to the top
	c.installRaw(gitops.DiffSpec{Path: "other.go"}, twoBlockRaw)
	assert.Equal(t, 1, c.diff.paneCursor())
}

func TestModel_SelectionGlyphRendering(t *testing.T) {
	store := annot.NewStore()
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{NoTree: true})
	m.filesLoaded = true
	next, _ := m.handleResize(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(Model)
	lines := []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "a", ChangeType: git.ChangeContext},
		{OldNum: 2, Content: "b", ChangeType: git.ChangeRemove},
		{NewNum: 2, Content: "B", ChangeType: git.ChangeAdd},
	}
	next, _ = m.handleFileLoaded(fileLoadedMsg{file: "f.go", seq: m.file.loadSeq, lines: lines})
	m = next.(Model)
	m.layout.focus = paneDiff
	m.nav.diffCursor = 0
	m.setPaneLineSelected(func(idx int) bool { return idx == 1 || idx == 2 })
	m.invalidateRenderCaches()
	out := m.renderDiff()
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, rows, 3)
	res := style.PlainResolver()
	mark := style.NewRenderer(res).SelectionMark(false)
	assert.NotContains(t, rows[0], mark, "cursor row shows the cursor, not the selection mark")
	assert.Contains(t, rows[1], mark)
	assert.Contains(t, rows[2], mark)
	assert.True(t, m.lineRenderFlags(1, m.decorations()).selected)
	assert.False(t, m.lineRenderFlags(0, m.decorations()).selected)

	// wrap mode paints the glyph on the first visual row only
	m.modes.wrap = true
	m.invalidateRenderCaches()
	out = m.renderDiff()
	assert.Contains(t, out, mark)

	// selection hook off: no marks
	m.setPaneLineSelected(nil)
	m.invalidateRenderCaches()
	assert.NotContains(t, m.renderDiff(), mark)
}

func TestRenderer_SelectionMark(t *testing.T) {
	r := style.NewRenderer(style.PlainResolver())
	assert.Equal(t, "▎", r.SelectionMark(false), "plain resolver has no accent color")
	assert.Equal(t, "\x1b[7m▎\x1b[27m", r.SelectionMark(true))
	colored := style.NewRenderer(style.NewResolver(style.Colors{Accent: "#ff0000"}))
	assert.Contains(t, colored.SelectionMark(false), "▎")
	assert.Contains(t, colored.SelectionMark(false), "\x1b[")
}

func TestCommitModel_StageHunkKey(t *testing.T) {
	c := newSelectCommit(t)
	c.Update(keyRunes("j"))
	c.Update(keyRunes("j")) // row 3 (+B): s stages the whole change block, as s marks it in review mode
	cmd := c.Update(keyRunes("s"))
	require.NotNil(t, cmd)
	cmd()
	calls := repoOf(c).ApplyLinesCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, []int{5, 6}, calls[0].Req.Indices)
	assert.Equal(t, selectLine, c.staging.mode)

	// with a range selected s acts on the range instead
	c.Update(keyRunes("V"))
	c.Update(keyRunes("j"))
	c.Update(keyRunes("j"))
	c.Update(keyRunes("j")) // rows 3..6
	cmd = c.Update(keyRunes("s"))
	cmd()
	assert.Equal(t, []int{6, 8, 9}, repoOf(c).ApplyLinesCalls()[1].Req.Indices)

	// J/K scroll the diff like in review mode and never start a selection
	c.Update(keyRunes("J"))
	c.Update(keyRunes("K"))
	assert.Equal(t, selectLine, c.staging.mode)
}

func TestCommitModel_StageHunkKeyOnFileRow(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c) // cursor on both.go in the Staged section
	runCmd(t, c, c.Update(keyRunes("s")))
	require.Len(t, repo.UnstageFilesCalls(), 1)
	assert.Equal(t, []string{"both.go"}, repo.UnstageFilesCalls()[0].Paths)
}
