package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/stageplan"
	"github.com/yousysadmin/igit/internal/tui/style"
)

// stageLines is a diff with two change blocks: rows 1-2 (-b/+B) and rows 4-5 (-d/+D).
func stageLines() []git.DiffLine {
	return []git.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "a", ChangeType: git.ChangeContext},
		{OldNum: 2, Content: "b", ChangeType: git.ChangeRemove},
		{NewNum: 2, Content: "B", ChangeType: git.ChangeAdd},
		{OldNum: 3, NewNum: 3, Content: "c", ChangeType: git.ChangeContext},
		{OldNum: 4, Content: "d", ChangeType: git.ChangeRemove},
		{NewNum: 4, Content: "D", ChangeType: git.ChangeAdd},
		{OldNum: 5, NewNum: 5, Content: "e", ChangeType: git.ChangeContext},
	}
}

// newStageModel returns a review model with a.go loaded in the diff pane and
// stage marks enabled.
func newStageModel(t *testing.T, applicable bool) Model {
	t.Helper()
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{Applicable: Applicable{StagePlan: applicable}})
	next, _ := m.handleResize(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	m.tree.Rebuild([]git.FileEntry{{Path: "a.go", Status: git.FileModified}, {Path: "b.go", Status: git.FileModified}})
	m.filesLoaded = true
	next, _ = m.handleFileLoaded(fileLoadedMsg{file: "a.go", seq: m.file.loadSeq, lines: stageLines()})
	m = next.(Model)
	m.layout.focus = paneDiff
	return m
}

func dispatch(t *testing.T, m Model, action keymap.Action) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.dispatchAction(action)
	got, ok := next.(Model)
	require.True(t, ok)
	return got, cmd
}

func TestStage_markBlockUnderCursor(t *testing.T) {
	m := newStageModel(t, true)
	m.nav.diffCursor = 2 // +B
	m, cmd := dispatch(t, m, keymap.ActionStageMark)
	assert.Nil(t, cmd)
	plan := m.StagePlan()
	assert.True(t, plan.Has("a.go"))
	assert.True(t, plan.Covers("a.go", 2, 0), "-b is in the block")
	assert.True(t, plan.Covers("a.go", 0, 2), "+B is in the block")
	assert.False(t, plan.Covers("a.go", 4, 0), "second block untouched")
	assert.Contains(t, m.transientHint(), "marked 2 line(s)")
	assert.True(t, m.stageMarked(1))
	assert.True(t, m.stageMarked(2))
	assert.False(t, m.stageMarked(0), "context rows are never marked")
	assert.False(t, m.stageMarked(4))

	// the stage glyph shows on marked rows, the tree shows the file marker, the status bar counts
	mark := style.NewRenderer(style.PlainResolver()).StageMark(false)
	m.stage.hint = ""
	view := m.View()
	assert.Contains(t, view, mark)
	assert.Contains(t, view, "a.go +")
	assert.Contains(t, view, "stage: 1")

	// toggling the same block removes it
	m, _ = dispatch(t, m, keymap.ActionStageMark)
	assert.False(t, m.StagePlan().Has("a.go"))
	assert.Equal(t, "mark removed", m.transientHint())

	// on a context row there is nothing to mark
	m.nav.diffCursor = 3
	m, _ = dispatch(t, m, keymap.ActionStageMark)
	assert.True(t, m.StagePlan().Empty())
	assert.Contains(t, m.transientHint(), "move the cursor onto a change")
}

func TestStage_visualRange(t *testing.T) {
	m := newStageModel(t, true)
	m.nav.diffCursor = 1
	m, _ = dispatch(t, m, keymap.ActionVisualRange)
	assert.Equal(t, 1, m.stage.rangeAnchor)
	m.nav.diffCursor = 5
	m.syncViewportToCursor()
	assert.True(t, m.inVisualRange(3))
	assert.True(t, m.isSelectedLine(4))
	assert.False(t, m.inVisualRange(6))
	assert.Contains(t, m.View(), style.NewRenderer(style.PlainResolver()).SelectionMark(false))

	m, _ = dispatch(t, m, keymap.ActionStageMark)
	assert.Equal(t, -1, m.stage.rangeAnchor, "marking consumes the range")
	marks, ok := m.StagePlan().Marks("a.go")
	require.True(t, ok)
	require.Len(t, marks.Ranges, 1)
	assert.Equal(t, stageplan.Range{OldStart: 2, OldEnd: 4, NewStart: 2, NewEnd: 4, Hash: marks.Ranges[0].Hash}, marks.Ranges[0])
	assert.Contains(t, m.transientHint(), "marked 5 line(s)")

	// V twice cancels the range
	m, _ = dispatch(t, m, keymap.ActionVisualRange)
	m, _ = dispatch(t, m, keymap.ActionVisualRange)
	assert.Equal(t, -1, m.stage.rangeAnchor)

	// a range of context lines only has nothing to mark
	m.nav.diffCursor = 3
	m, _ = dispatch(t, m, keymap.ActionVisualRange)
	m, _ = dispatch(t, m, keymap.ActionStageMark)
	assert.Contains(t, m.transientHint(), "no changed lines")

	// range selection needs the diff pane
	m.layout.focus = paneTree
	m, _ = dispatch(t, m, keymap.ActionVisualRange)
	assert.Contains(t, m.transientHint(), "diff pane")
	assert.False(t, m.inVisualRange(3), "no selection painted while the tree has focus")
}

func TestStage_markWholeFile(t *testing.T) {
	m := newStageModel(t, true)
	m, _ = dispatch(t, m, keymap.ActionStageMarkFile)
	assert.True(t, m.StagePlan().IsWhole("a.go"))
	assert.True(t, m.stageMarked(1))
	assert.True(t, m.stageMarked(5))
	assert.Contains(t, m.transientHint(), "marked a.go")
	m, _ = dispatch(t, m, keymap.ActionStageMarkFile)
	assert.False(t, m.StagePlan().Has("a.go"))

	// from the tree, s marks the selected file as a whole
	m.layout.focus = paneTree
	m.tree.SelectByPath("b.go")
	m, _ = dispatch(t, m, keymap.ActionStageMark)
	assert.True(t, m.StagePlan().IsWhole("b.go"))
	assert.Contains(t, m.View(), "b.go +")
	assert.NotContains(t, m.View(), "a.go +")
}

func TestStage_unavailable(t *testing.T) {
	m := newStageModel(t, false)
	for _, a := range []keymap.Action{keymap.ActionStageMark, keymap.ActionStageMarkFile, keymap.ActionVisualRange, keymap.ActionCommitWithPlan} {
		var cmd tea.Cmd
		m, cmd = dispatch(t, m, a)
		assert.Nil(t, cmd)
		assert.Contains(t, m.transientHint(), "git working-tree review", string(a))
	}
	assert.True(t, m.StagePlan().Empty())
}

func TestStage_commitWithPlanEmitsMessage(t *testing.T) {
	m := newStageModel(t, true)
	m.nav.diffCursor = 1
	m, _ = dispatch(t, m, keymap.ActionVisualRange)
	m, _ = dispatch(t, m, keymap.ActionStageMarkFile)
	m, cmd := dispatch(t, m, keymap.ActionCommitWithPlan)
	require.NotNil(t, cmd)
	msg, ok := cmd().(commitWithPlanMsg)
	require.True(t, ok)
	assert.Same(t, m.StagePlan(), msg.plan)
	assert.Equal(t, -1, m.stage.rangeAnchor, "a pending range is dropped")
}

func TestStage_quitConfirmation(t *testing.T) {
	m := newStageModel(t, true)
	// nothing marked: q quits at once
	_, cmd := dispatch(t, m, keymap.ActionQuit)
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())

	m, _ = dispatch(t, m, keymap.ActionStageMarkFile)
	m, cmd = dispatch(t, m, keymap.ActionQuit)
	assert.Nil(t, cmd)
	assert.True(t, m.stage.confirmQuit)
	assert.Contains(t, m.statusBarText(), "1 file(s) marked")
	assert.True(t, m.inputBusy(), "the App must not intercept keys while confirming")

	// any other key stays
	next, cmd := m.Update(keyRunes("j"))
	m = next.(Model)
	assert.Nil(t, cmd)
	assert.False(t, m.stage.confirmQuit)
	assert.True(t, m.StagePlan().Has("a.go"))

	m, _ = dispatch(t, m, keymap.ActionQuit)
	next, cmd = m.Update(keyRunes("y"))
	m = next.(Model)
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	assert.False(t, m.stage.confirmQuit)

	// no status bar: nothing to confirm with, quit directly
	m.cfg.noStatusBar = true
	_, cmd = dispatch(t, m, keymap.ActionQuit)
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestStage_removeStagedFromPlan(t *testing.T) {
	m := newStageModel(t, true)
	m.StagePlan().ToggleFile("a.go")
	m.StagePlan().ToggleFile("b.go")
	m.removeStagedFromPlan(stageplan.Result{Staged: []string{"a.go"}, Skipped: []stageplan.Skipped{{Path: "b.go", Reason: "drift"}}})
	assert.False(t, m.StagePlan().Has("a.go"))
	assert.True(t, m.StagePlan().Has("b.go"), "skipped files keep their marks")
}

func TestStage_planSurvivesReload(t *testing.T) {
	m := newStageModel(t, true)
	m.StagePlan().ToggleFile("a.go")
	m.session.noConfirmReload = true
	m.reload.applicable = true
	m, cmd := dispatch(t, m, keymap.ActionReload)
	assert.NotNil(t, cmd)
	assert.True(t, m.StagePlan().Has("a.go"))
}
