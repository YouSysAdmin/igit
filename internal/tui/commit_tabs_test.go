package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// press sends a key and drains the resulting commands synchronously.
func press(t *testing.T, c *CommitModel, k string) {
	t.Helper()
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = keyRunes(k)
	}
	drainCommit(t, c, c.Update(msg))
}

func drainCommit(t *testing.T, c *CommitModel, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == nil {
			continue
		}
		msg := cur()
		switch m := msg.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, m...)
		default:
			queue = append(queue, c.Update(msg))
		}
	}
}

func TestCommitModel_TabSwitching(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	assert.Contains(t, c.View(), " Files ")
	assert.Contains(t, c.View(), " Stash ")

	press(t, c, "2")
	assert.Equal(t, tabBranches, c.tab)
	assert.Len(t, repo.BranchesCalls(), 1)
	view := c.View()
	assert.Contains(t, view, "Local (2)")
	assert.Contains(t, view, "Remote (1)")
	assert.Contains(t, view, "* main ↑1 ↓0")
	assert.Contains(t, view, "feature (gone)")
	assert.Contains(t, view, "[enter] checkout")
	assert.Contains(t, view, "no file selected", "branches show no diff")

	press(t, c, "3")
	assert.Equal(t, tabLog, c.tab)
	view = c.View()
	assert.Contains(t, view, "1111111 (main) second")
	assert.Equal(t, "second\n\nlonger body", c.hist.note, "the commit message becomes the diff header note")
	assert.Contains(t, view, "  longer body")
	assert.Contains(t, view, "2222222 first")
	assert.Len(t, repo.RangeDiffCalls(), 1, "the selected commit's diff is fetched")
	assert.Equal(t, "2222222bbbbbbb..1111111aaaaaaa", repo.RangeDiffCalls()[0].Ref)
	assert.Equal(t, "1111111 second", c.diff.file.name, "the header names the commit")
	assert.Empty(t, repo.CommitFilesCalls(), "the file list waits for enter")

	press(t, c, "4")
	assert.Equal(t, tabStash, c.tab)
	view = c.View()
	assert.Contains(t, view, "{0} On main: wip")
	assert.Contains(t, view, "{1} older")
	assert.Len(t, repo.RangeDiffCalls(), 2, "the stash entry's diff is fetched")

	press(t, c, ">")
	assert.Equal(t, tabFiles, c.tab, "wraps around")
	press(t, c, "<")
	assert.Equal(t, tabStash, c.tab)
	press(t, c, "1")
	assert.Equal(t, tabFiles, c.tab)
	assert.Equal(t, "both.go", c.staging.spec.Path, "files tab restores the staging diff")

	// switching back to a loaded tab does not reload it
	press(t, c, "2")
	assert.Len(t, repo.BranchesCalls(), 1)
}

func TestCommitModel_LogDetailAndDiff(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	// the embedded diff pane reads per-file history diffs through the review renderer
	rendered := &git.FileDiffRequest{}
	c.diff.diffSource = &fakeRenderer{onFileDiff: func(req git.FileDiffRequest) { *rendered = req }}
	loadAll(t, c)

	// selecting a commit shows its whole diff, every file at once
	press(t, c, "3")
	require.Len(t, repo.RangeDiffCalls(), 1)
	assert.Equal(t, "2222222bbbbbbb..1111111aaaaaaa", repo.RangeDiffCalls()[0].Ref)
	assert.Equal(t, "1111111 second", c.diff.file.name, "the header names the commit, not a file")
	view := c.View()
	for _, want := range []string{"a.go", "old.txt -> new.txt", "gone.md (deleted)", "logo.png"} {
		assert.Contains(t, view, want)
	}
	assert.Empty(t, rendered.Path, "no per-file request while the commit is only selected")

	// enter drills into the file list, j moves to the second file, esc goes back
	press(t, c, "enter")
	require.NotNil(t, c.hist.detail)
	view = c.View()
	assert.Contains(t, view, "1111111 second")
	assert.Contains(t, view, "b.go")
	press(t, c, "j")
	assert.Equal(t, "b.go", rendered.Path)
	assert.Equal(t, "b.go", c.diff.file.name)
	press(t, c, "enter")
	assert.Equal(t, focusDiff, c.focus)
	assert.Nil(t, c.Update(keyRunes(" ")))
	assert.Contains(t, c.statusBarText(), "Files tab", "no line staging on history diffs")
	press(t, c, "esc")
	assert.Equal(t, focusSide, c.focus)
	press(t, c, "esc")
	assert.Nil(t, c.hist.detail)

	// moving to the older commit loads its diff
	press(t, c, "j")
	last := repo.RangeDiffCalls()[len(repo.RangeDiffCalls())-1]
	assert.Equal(t, "4b825dc642cb6eb9a060e54bf8d69288fbee4904..2222222bbbbbbb", last.Ref)
}

// fakeRenderer records FileDiff requests and returns a fixed diff.
type fakeRenderer struct {
	onFileDiff func(git.FileDiffRequest)
}

func (f *fakeRenderer) ChangedFiles(string, bool) ([]git.FileEntry, error) { return nil, nil }
func (f *fakeRenderer) FileDiff(req git.FileDiffRequest) ([]git.DiffLine, error) {
	if f.onFileDiff != nil {
		f.onFileDiff(req)
	}
	return []git.DiffLine{{OldNum: 1, NewNum: 1, Content: "x", ChangeType: git.ChangeContext}, {NewNum: 2, Content: "y", ChangeType: git.ChangeAdd}}, nil
}

func TestCommitModel_BranchActions(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	press(t, c, "2")

	// enter on HEAD does nothing. on another branch it confirms a checkout
	press(t, c, "enter")
	assert.False(t, c.overlay.Active())
	press(t, c, "j") // feature
	press(t, c, "enter")
	require.Equal(t, overlay.KindConfirm, c.overlay.Kind())
	assert.Contains(t, c.View(), "Check out feature?")
	press(t, c, "y")
	require.Len(t, repo.CheckoutCalls(), 1)
	assert.Equal(t, "feature", repo.CheckoutCalls()[0].Name)
	assert.False(t, repo.CheckoutCalls()[0].Force)

	// new branch from the selected one
	press(t, c, "b")
	require.Equal(t, overlay.KindPrompt, c.overlay.Kind())
	assert.Contains(t, c.View(), "new branch from feature")
	press(t, c, "topic")
	press(t, c, "enter")
	require.Len(t, repo.CreateBranchCalls(), 1)
	assert.Equal(t, "topic", repo.CreateBranchCalls()[0].Name)
	assert.Equal(t, "feature", repo.CreateBranchCalls()[0].Base)
	assert.True(t, repo.CreateBranchCalls()[0].Checkout)

	// rename, merge, upstream
	press(t, c, "r")
	assert.Equal(t, "feature", c.overlay.(*overlay.Manager).PromptValue())
	press(t, c, "2")
	press(t, c, "enter")
	assert.Equal(t, "feature2", repo.RenameBranchCalls()[0].NewName)

	press(t, c, "m")
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	press(t, c, "f")
	require.Len(t, repo.MergeCalls(), 1)
	assert.Equal(t, "feature", repo.MergeCalls()[0].Name)
	assert.True(t, repo.MergeCalls()[0].O.FFOnly)

	press(t, c, "U")
	assert.Equal(t, "origin/feature", c.overlay.(*overlay.Manager).PromptValue())
	press(t, c, "enter")
	require.Len(t, repo.SetUpstreamCalls(), 1)
	assert.Equal(t, "origin", repo.SetUpstreamCalls()[0].Remote)
	assert.Equal(t, "feature", repo.SetUpstreamCalls()[0].RemoteBranch)
	press(t, c, "U")
	c.overlay.(*overlay.Manager).HandleKey(tea.KeyMsg{Type: tea.KeyCtrlU}, "")
	press(t, c, "esc")
	assert.Nil(t, c.handlePromptSubmittedForTest("upstream", "nonsense"))
	assert.Contains(t, c.statusBarText(), "remote/branch")

	// delete: menu, then force variant
	press(t, c, "d")
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	press(t, c, "D")
	require.Len(t, repo.DeleteBranchCalls(), 1)
	assert.True(t, repo.DeleteBranchCalls()[0].Force)

	// HEAD and remote branches refuse deletion
	press(t, c, "k")
	press(t, c, "d")
	assert.False(t, c.overlay.Active())
	assert.Contains(t, c.statusBarText(), "checked-out")
	press(t, c, "j")
	press(t, c, "j") // origin/main
	press(t, c, "d")
	assert.Contains(t, c.statusBarText(), "remote branches")
	press(t, c, "a")
	assert.Contains(t, c.statusBarText(), "Files tab")
	press(t, c, " ")
	assert.Contains(t, c.statusBarText(), "not available")
}

// handlePromptSubmittedForTest exposes the prompt handler with a pending branch.
func (c *CommitModel) handlePromptSubmittedForTest(id, value string) tea.Cmd {
	c.pending = pendingAction{id: id, name: "feature"}
	return c.handlePromptSubmitted(id, value)
}

func TestCommitModel_StashActions(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)

	// S from the files tab: stash menu -> message prompt -> push
	press(t, c, "S")
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	assert.Contains(t, c.View(), "stash only the staged changes")
	press(t, c, "u")
	require.Equal(t, overlay.KindPrompt, c.overlay.Kind())
	press(t, c, "wip")
	press(t, c, "enter")
	require.Len(t, repo.StashPushCalls(), 1)
	assert.Equal(t, gitops.StashPushOpts{Message: "wip", IncludeUntracked: true}, repo.StashPushCalls()[0].O)

	// stash tab: pop/apply/drop
	press(t, c, "4")
	press(t, c, "S")
	assert.Contains(t, c.View(), "pop stash@{0}")
	press(t, c, "p")
	require.Len(t, repo.StashPopCalls(), 1)
	assert.Equal(t, 0, repo.StashPopCalls()[0].Index)
	press(t, c, "S")
	press(t, c, "y")
	require.Len(t, repo.StashApplyCalls(), 1)
	press(t, c, "j")
	press(t, c, "d")
	require.Equal(t, overlay.KindConfirm, c.overlay.Kind())
	assert.Contains(t, c.View(), "Drop stash@{1}")
	press(t, c, "y")
	require.Len(t, repo.StashDropCalls(), 1)
	assert.Equal(t, 1, repo.StashDropCalls()[0].Index)

	// b on the stash tab opens the stash menu, keep-index variant
	press(t, c, "b")
	press(t, c, "k")
	press(t, c, "enter")
	assert.True(t, repo.StashPushCalls()[1].O.KeepIndex)
	press(t, c, "S")
	press(t, c, "s")
	press(t, c, "enter")
	assert.True(t, repo.StashPushCalls()[2].O.Staged)

	// detail view of the selected stash
	press(t, c, "enter")
	require.NotNil(t, c.hist.detail)
	assert.Contains(t, c.View(), "stash@{1} older")
}

func TestCommitModel_HistoryLoadErrors(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	repo.BranchesFunc = func(context.Context) ([]gitops.Branch, error) { return nil, errors.New("boom") }
	repo.LogFunc = func(context.Context, string, int) ([]gitops.Commit, error) { return nil, nil }
	c := newTestCommit(t, repo)
	loadAll(t, c)
	press(t, c, "2")
	assert.Equal(t, overlay.KindError, c.overlay.Kind())
	press(t, c, "esc")
	press(t, c, "3")
	assert.Contains(t, c.View(), "no commits")
	press(t, c, "enter")
	assert.Nil(t, c.hist.detail)
	// stale messages are ignored
	c.Update(commitLogMsg{seq: 0, commits: []gitops.Commit{{Hash: "x", ShortHash: "x", Subject: "stale"}}})
	assert.NotContains(t, c.View(), "stale")
}

func TestCommitModel_SyncActions(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)

	press(t, c, "f")
	require.NotNil(t, launched)
	assert.Equal(t, []string{"true", "fetch"}, launched.Args)
	assert.Contains(t, c.statusBarText(), "fetch done")
	assert.NotNil(t, launched.Stderr, "stderr is mirrored for the summary")

	press(t, c, "F")
	assert.Equal(t, []string{"true", "pull", ""}, launched.Args)
	assert.Contains(t, c.statusBarText(), "pull done")

	// push with an upstream: menu offers plain and force push
	press(t, c, "P")
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	assert.Contains(t, c.View(), "push to origin/main")
	press(t, c, "p")
	require.Len(t, repo.PushCmdCalls(), 1)
	assert.Equal(t, gitops.PushOpts{}, repo.PushCmdCalls()[0].O)
	press(t, c, "P")
	press(t, c, "f")
	assert.True(t, repo.PushCmdCalls()[1].O.ForceWithLease)

	// no upstream: pull refuses, push sets one
	c.status.Head.Upstream = ""
	press(t, c, "F")
	assert.Contains(t, c.statusBarText(), "no upstream")
	press(t, c, "P")
	assert.Contains(t, c.View(), "set upstream (origin/main)")
	press(t, c, "u")
	assert.Equal(t, gitops.PushOpts{Remote: "origin", SetUpstream: true}, repo.PushCmdCalls()[2].O)

	// a failing sync opens the error popup with the captured stderr
	c.run = func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			_, _ = cmd.Stderr.Write([]byte("fatal: could not read from remote\n"))
			return done(errors.New("exit status 128"))
		}
	}
	press(t, c, "f")
	require.Equal(t, overlay.KindError, c.overlay.Kind())
	assert.Contains(t, c.View(), "could not read from remote")

	c.status.Head.Detached = true
	press(t, c, "esc")
	press(t, c, "P")
	assert.Contains(t, c.statusBarText(), "nothing to push")
}

func TestTabForAction(t *testing.T) {
	assert.Equal(t, "Files", tabFiles.String())
	assert.Equal(t, "Stash", tabStash.String())
	assert.Equal(t, "?", commitTab(9).String())
	assert.Equal(t, "HEAD", orHead(""))
}

func TestCommitModel_BranchRebase(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	press(t, c, "2")
	press(t, c, "j") // feature

	press(t, c, "o")
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	assert.Contains(t, c.View(), "onto feature")

	press(t, c, "r")
	require.Len(t, repo.RebaseCalls(), 1)
	assert.Equal(t, "feature", repo.RebaseCalls()[0].Onto)
	assert.False(t, repo.RebaseCalls()[0].O.Autostash)

	press(t, c, "o")
	press(t, c, "a")
	require.Len(t, repo.RebaseCalls(), 2)
	assert.True(t, repo.RebaseCalls()[1].O.Autostash, "the second variant stashes first")
}

func TestCommitModel_BranchReset(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	press(t, c, "2")
	press(t, c, "j") // feature

	press(t, c, "g")
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	press(t, c, "m")
	require.Len(t, repo.ResetCalls(), 1)
	assert.Equal(t, "feature", repo.ResetCalls()[0].Ref)
	assert.Equal(t, gitops.ResetMixed, repo.ResetCalls()[0].Mode)

	press(t, c, "g")
	press(t, c, "s")
	require.Len(t, repo.ResetCalls(), 2)
	assert.Equal(t, gitops.ResetSoft, repo.ResetCalls()[1].Mode)

	// hard throws away uncommitted work, so it asks a second time
	press(t, c, "g")
	press(t, c, "h")
	require.Equal(t, overlay.KindConfirm, c.overlay.Kind())
	assert.Len(t, repo.ResetCalls(), 2, "nothing ran on the menu choice alone")
	press(t, c, "n")
	assert.Len(t, repo.ResetCalls(), 2, "declining leaves the branch alone")

	press(t, c, "g")
	press(t, c, "h")
	press(t, c, "y")
	require.Len(t, repo.ResetCalls(), 3)
	assert.Equal(t, gitops.ResetHard, repo.ResetCalls()[2].Mode)
}

func TestCommitModel_BranchTargetRefusals(t *testing.T) {
	t.Run("the current branch is not a target", func(t *testing.T) {
		c := newTestCommit(t, newRepoMock(sampleStatus()))
		loadAll(t, c)
		press(t, c, "2")
		press(t, c, "o")
		assert.False(t, c.overlay.Active())
		assert.Contains(t, c.statusBarText(), "current branch")
	})

	t.Run("on the files tab the key points at the branches tab", func(t *testing.T) {
		c := newTestCommit(t, newRepoMock(sampleStatus()))
		loadAll(t, c)
		for _, key := range []string{"g", "o", "m", "U"} {
			press(t, c, key)
			assert.False(t, c.overlay.Active())
			assert.Contains(t, c.statusBarText(), "works on the branches tab", "key %q", key)
		}
	})

	t.Run("an unfinished operation blocks both", func(t *testing.T) {
		st := sampleStatus()
		st.InProgress = gitops.InProgress{State: gitops.StateRebasing}
		c := newTestCommit(t, newRepoMock(st))
		loadAll(t, c)
		press(t, c, "2")
		press(t, c, "j")
		press(t, c, "o")
		assert.False(t, c.overlay.Active())
		assert.Contains(t, c.statusBarText(), "rebasing")
	})
}

// manyPatchFiles builds n single-hunk file sections of a patch.
func manyPatchFiles(n int) []patchFile {
	files := make([]patchFile, n)
	for i := range files {
		name := fmt.Sprintf("f%02d.go", i)
		files[i] = patchFile{
			display: name, path: name,
			raw: "diff --git a/" + name + " b/" + name + "\n--- a/" + name + "\n+++ b/" + name +
				"\n@@ -1,1 +1,1 @@\n-old\n+new\n",
		}
	}
	return files
}

func TestCommitModel_EntryDiffFileCap(t *testing.T) {
	headers := func(lines []git.DiffLine) []string {
		var out []string
		for _, dl := range lines {
			if dl.IsFileHeader {
				out = append(out, dl.Content)
			}
		}
		return out
	}

	t.Run("stops after the cap and points at the file list", func(t *testing.T) {
		c := newTestCommit(t, newRepoMock(sampleStatus()))
		c.logDiffFiles = 3

		lines, highlighted := c.entryDiffLines(manyPatchFiles(10))
		require.Len(t, highlighted, len(lines), "every row keeps a highlighted twin")

		h := headers(lines)
		require.Len(t, h, 4, "three files plus the notice")
		assert.Equal(t, []string{"f00.go", "f01.go", "f02.go"}, h[:3])
		assert.Equal(t, "7 more files, press enter to browse them", h[3])
	})

	t.Run("an entry within the cap is shown whole", func(t *testing.T) {
		c := newTestCommit(t, newRepoMock(sampleStatus()))
		c.logDiffFiles = 10

		lines, _ := c.entryDiffLines(manyPatchFiles(4))
		h := headers(lines)
		assert.Len(t, h, 4)
		assert.NotContains(t, h[len(h)-1], "more files")
	})

	t.Run("zero means no cap", func(t *testing.T) {
		c := newTestCommit(t, newRepoMock(sampleStatus()))
		c.logDiffFiles = 0

		lines, _ := c.entryDiffLines(manyPatchFiles(40))
		assert.Len(t, headers(lines), 40)
	})
}
