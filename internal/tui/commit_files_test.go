package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/conflict"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

func TestCommitModel_FileFilterScopesListAndStageAll(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	var staged, unstaged []string
	repo.StageFilesFunc = func(_ context.Context, paths []string) error { staged = paths; return nil }
	repo.UnstageFilesFunc = func(_ context.Context, paths []string, _ bool) error { unstaged = paths; return nil }
	cfg := testCommitConfig()
	cfg.Repo = repo
	cfg.FileFilter = func(p string) bool { return p != "new.txt" && p != "staged.go" }
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	c.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	loadAll(t, c)

	var paths []string
	for _, r := range c.list.Rows() {
		if !r.Section {
			paths = append(paths, r.Key)
		}
	}
	assert.Equal(t, []string{"s:both.go", "u:a.go", "u:both.go"}, paths)

	runCmd(t, c, c.Update(keyRunes("a")))
	assert.Equal(t, []string{"a.go", "both.go"}, staged, "stage all is limited to the visible unstaged files")
	assert.Empty(t, repo.StageAllCalls())
	runCmd(t, c, c.Update(keyRunes("u")))
	assert.Equal(t, []string{"both.go"}, unstaged)
	assert.Empty(t, repo.UnstageAllCalls())

	// nothing visible in a section: a hint instead of a git call
	c.status.Files = []gitops.StatusEntry{{Path: "new.txt", Untracked: true, Index: '?', Worktree: '?'}}
	c.rebuildRows()
	assert.Nil(t, c.Update(keyRunes("a")))
	assert.Contains(t, c.statusBarText(), "file scope")
	assert.Contains(t, c.emptyListText(), "file scope")
}

func TestCommitModel_StageAllWithoutFilterUsesGitAddAll(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	runCmd(t, c, c.Update(keyRunes("a")))
	assert.Len(t, repo.StageAllCalls(), 1)
	assert.Empty(t, repo.StageFilesCalls())
}

func TestCommitModel_CrossFileHunks(t *testing.T) {
	cfg := testCommitConfig()
	cfg.Repo = newRepoMock(sampleStatus())
	cfg.DiffPane.CrossFileHunks = true
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	c.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	loadAll(t, c)
	assert.False(t, c.diff.session.crossFileHunks, "the embedded model never steps its own (empty) tree")
	// rows: 0 Staged header, 1 both.go, 2 staged.go, ...
	assert.Equal(t, 1, c.list.CursorIndex())

	assert.Nil(t, c.Update(keyRunes("]"))) // first hunk of the current file: row 2 (-two)
	assert.Equal(t, 2, c.diff.paneCursor())
	cmd := c.Update(keyRunes("]")) // no further hunk: step to the next file
	require.NotNil(t, cmd)
	assert.Equal(t, 2, c.list.CursorIndex())
	runCmd(t, c, cmd)
	assert.Equal(t, "staged.go", c.staging.spec.Path)
	assert.Equal(t, 2, c.diff.paneCursor(), "lands on the first hunk of the next file")
	assert.Nil(t, c.diff.nav.pendingHunkJump)

	cmd = c.Update(keyRunes("[")) // already on the first hunk: back to the previous file's last hunk
	require.NotNil(t, cmd)
	runCmd(t, c, cmd)
	assert.Equal(t, 1, c.list.CursorIndex())
	assert.Equal(t, "both.go", c.staging.spec.Path)
	assert.Equal(t, 2, c.diff.paneCursor())

	// at the very first file a backward jump has nowhere to go
	assert.Nil(t, c.Update(keyRunes("[")))
	assert.Nil(t, c.diff.nav.pendingHunkJump)

	// entry lists (log tab) never step across commits
	c.Update(keyRunes("3"))
	assert.Nil(t, c.Update(keyRunes("]")))
}

func TestCommitModel_CrossFileHunksOffStaysOnFile(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.Update(keyRunes("]"))
	assert.Nil(t, c.Update(keyRunes("]")))
	assert.Equal(t, 1, c.list.CursorIndex())
}

// mergingStatus is a working tree stuck in a conflicted merge.
func mergingStatus() gitops.Status {
	st := sampleStatus()
	st.InProgress = gitops.InProgress{State: gitops.StateMerging}
	st.Files = append(st.Files, gitops.StatusEntry{Path: "conflict.go", Index: 'U', Worktree: 'U', Conflict: true, ConflictXY: "UU"})
	return st
}

func TestCommitModel_UnstageAllRefusedDuringAMerge(t *testing.T) {
	// `git reset` drops every merge stage and MERGE_HEAD with them, which
	// leaves a tree that can be neither resolved nor aborted
	repo := newRepoMock(mergingStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)

	assert.Nil(t, c.Update(keyRunes("u")))
	assert.Empty(t, repo.UnstageAllCalls(), "nothing is reset")
	assert.Empty(t, repo.UnstageFilesCalls())
	hint := c.statusBarText()
	assert.Contains(t, hint, "would destroy the merging state")
	assert.Contains(t, hint, "abort with M", "the way out is named")

	// staging all is still allowed, marking every file resolved is legitimate
	runCmd(t, c, c.Update(keyRunes("a")))
	assert.Len(t, repo.StageAllCalls(), 1)
}

func TestCommitModel_UnstageAllAllowedWithoutAMerge(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	runCmd(t, c, c.Update(keyRunes("u")))
	assert.Len(t, repo.UnstageAllCalls(), 1)
}

func TestCommitModel_UnstagingDuringAMergeRestoresTheConflict(t *testing.T) {
	repo := newRepoMock(mergingStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)

	require.True(t, c.list.SelectByKey("s:staged.go"), "the staged row is in the list")
	runCmd(t, c, c.Update(keyRunes(" ")))

	require.Len(t, repo.UnstageDuringMergeCalls(), 1, "the merge-aware path is used")
	assert.Equal(t, "staged.go", repo.UnstageDuringMergeCalls()[0].Path)
	assert.Empty(t, repo.UnstageFilesCalls(), "the plain reset is not used mid-merge")
}

func TestCommitModel_DiffPaneResolvesOneConflictBlock(t *testing.T) {
	repo := newRepoMock(mergingStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	require.True(t, c.list.SelectByKey("c:conflict.go"))
	c.staging.spec.Path = "conflict.go"
	c.focus = focusDiff
	// the diff cursor sits on a line inside the block the mock reports
	c.diff.file.lines = []git.DiffLine{{NewNum: 3, Content: "ours", ChangeType: git.ChangeAdd}}
	c.diff.nav.diffCursor = 0

	assert.Nil(t, c.Update(keyRunes(" ")))
	require.Equal(t, overlay.KindMenu, c.overlay.Kind(), "the sides are offered, not staged")
	view := c.View()
	assert.Contains(t, view, "conflict 1/1 in conflict.go")
	assert.Contains(t, view, "keep ours (HEAD)", "the branch git named is shown")
	assert.Contains(t, view, "keep theirs (feature)")
	assert.Contains(t, view, "keep both")
	assert.Empty(t, repo.ApplyLinesCalls(), "no patch is applied to an unmerged path")

	runCmd(t, c, c.Update(keyRunes("t")))
	require.Len(t, repo.ResolveConflictRegionCalls(), 1)
	call := repo.ResolveConflictRegionCalls()[0]
	assert.Equal(t, "conflict.go", call.Path)
	assert.Equal(t, 0, call.Index)
	assert.Equal(t, conflict.Theirs, call.Choice)
}

func TestCommitModel_DiffPaneConflictCursorOutsideABlock(t *testing.T) {
	repo := newRepoMock(mergingStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	c.staging.spec.Path = "conflict.go"
	c.focus = focusDiff
	// line 9 is past the block the mock reports
	c.diff.file.lines = []git.DiffLine{{NewNum: 9, Content: "tail", ChangeType: git.ChangeContext}}
	c.diff.nav.diffCursor = 0

	assert.Nil(t, c.Update(keyRunes(" ")))
	assert.False(t, c.overlay.Active(), "nothing to choose between")
	assert.Contains(t, c.statusBarText(), "no conflict on this line")
	assert.Contains(t, c.statusBarText(), "1 conflict")
	assert.Empty(t, repo.ResolveConflictRegionCalls())
}
