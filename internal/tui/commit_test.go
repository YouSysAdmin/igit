package tui

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/conflict"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/mocks"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

const sampleRaw = "diff --git a/a.go b/a.go\nindex 1..2 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"

// sampleStatus has one staged, one modified (unstaged), one untracked and one both-sides file.
func sampleStatus() gitops.Status {
	return gitops.Status{
		Head: gitops.BranchHead{Name: "main", OID: "0123456789abcdef", Upstream: "origin/main", Ahead: 1},
		Files: []gitops.StatusEntry{
			{Path: "staged.go", Index: 'M', Worktree: gitops.Unmodified},
			{Path: "a.go", Index: gitops.Unmodified, Worktree: 'M'},
			{Path: "new.txt", Untracked: true, Index: '?', Worktree: '?'},
			{Path: "both.go", Index: 'M', Worktree: 'M'},
		},
		StashCount: 2,
	}
}

func newRepoMock(st gitops.Status) *mocks.RepoMock {
	return &mocks.RepoMock{
		StatusFunc:      func(context.Context) (gitops.Status, error) { return st, nil },
		StagingDiffFunc: func(context.Context, gitops.DiffSpec) (string, error) { return sampleRaw, nil },
		StageFilesFunc:  func(context.Context, []string) error { return nil },
		StageAllFunc:    func(context.Context) error { return nil },
		UnstageFilesFunc: func(context.Context, []string, bool) error {
			return nil
		},
		UnstageAllFunc:         func(context.Context, bool) error { return nil },
		UnstageDuringMergeFunc: func(context.Context, string, bool) error { return nil },
		ResolveConflictFunc:    func(context.Context, string, bool) error { return nil },
		ConflictRegionsFunc: func(string) ([]conflict.Region, error) {
			return []conflict.Region{{
				Start: 2, End: 6, OursLabel: "HEAD", TheirsLabel: "feature",
				Ours: []string{"ours"}, Theirs: []string{"theirs"},
			}}, nil
		},
		ResolveConflictRegionFunc: func(string, int, conflict.Choice) error { return nil },
		AbortOperationFunc:        func(context.Context, gitops.RepoState) error { return nil },
		ContinueCmdFunc: func(gitops.RepoState) (*exec.Cmd, error) {
			return exec.Command("true", "continue"), nil
		},
		CheckoutRemoteFunc: func(context.Context, string, bool) error { return nil },
		DiscardFunc:        func(context.Context, gitops.StatusEntry, gitops.DiscardScope, bool) error { return nil },
		ApplyLinesFunc:     func(context.Context, gitops.LineRequest) error { return nil },
		CommitFunc:         func(context.Context, string, gitops.CommitOpts) error { return nil },
		CommitEditorCmdFunc: func(o gitops.CommitOpts, seed string) (*exec.Cmd, func(), error) {
			return exec.Command("true", "editor", seed), func() {}, nil //nolint:gosec // test stub, fixed program
		},
		CommitFileCmdFunc: func(o gitops.CommitOpts, msg string) (*exec.Cmd, func(), error) {
			return exec.Command("true", "file", msg), func() {}, nil //nolint:gosec // test stub, fixed program
		},
		LastCommitMessageFunc: func(context.Context) (string, error) { return "last subject\n\nlast body", nil },
		CommitMessagesFunc: func(context.Context, int) ([]string, error) {
			return []string{"last subject\n\nlast body", "older"}, nil
		},
		HasStagedChangesFunc: func(context.Context) (bool, error) { return true, nil },
		GPGSignEnabledFunc:   func(context.Context) bool { return false },
		BranchesFunc: func(context.Context) ([]gitops.Branch, error) {
			return []gitops.Branch{
				{Name: "main", Head: true, Upstream: "origin/main", Ahead: 1, Hash: "aaa", Subject: "s"},
				{Name: "feature", Hash: "bbb", Subject: "f", UpstreamGone: true},
				{Name: "origin/main", Remote: true, Hash: "aaa", Subject: "s"},
			}, nil
		},
		LogFunc: func(context.Context, string, int) ([]gitops.Commit, error) {
			return []gitops.Commit{
				{Hash: "1111111aaaaaaa", ShortHash: "1111111", Subject: "second", Body: "longer body", Parents: []string{"2222222bbbbbbb"}, Refs: []string{"main"}},
				{Hash: "2222222bbbbbbb", ShortHash: "2222222", Subject: "first"},
			}, nil
		},
		StashesFunc: func(context.Context) ([]gitops.StashEntry, error) {
			return []gitops.StashEntry{{Index: 0, Hash: "s0", Message: "On main: wip"}, {Index: 1, Hash: "s1", Message: "older"}}, nil
		},
		StashFilesFunc: func(context.Context, int) ([]git.FileEntry, error) {
			return []git.FileEntry{{Path: "a.go", Status: git.FileModified}}, nil
		},
		RangeDiffFunc: func(context.Context, string, int) (string, error) {
			return multiFilePatch, nil
		},
		CommitFilesFunc: func(context.Context, string) ([]git.FileEntry, error) {
			return []git.FileEntry{{Path: "a.go", Status: git.FileModified}, {Path: "b.go", Status: git.FileAdded}}, nil
		},
		StashStagedSupportedFunc: func(context.Context) bool { return true },
		StashPushFunc:            func(context.Context, gitops.StashPushOpts) error { return nil },
		StashPopFunc:             func(context.Context, int) error { return nil },
		StashApplyFunc:           func(context.Context, int) error { return nil },
		StashDropFunc:            func(context.Context, int) error { return nil },
		CheckoutFunc:             func(context.Context, string, bool) error { return nil },
		CreateBranchFunc:         func(context.Context, string, string, bool) error { return nil },
		DeleteBranchFunc:         func(context.Context, string, bool) error { return nil },
		RenameBranchFunc:         func(context.Context, string, string) error { return nil },
		MergeFunc:                func(context.Context, string, gitops.MergeOpts) error { return nil },
		RebaseFunc:               func(context.Context, string, gitops.RebaseOpts) error { return nil },
		ResetFunc:                func(context.Context, string, gitops.ResetMode) error { return nil },
		SetUpstreamFunc:          func(context.Context, string, string, string) error { return nil },
		RemotesFunc:              func(context.Context) ([]string, error) { return []string{"origin"}, nil },
		PushCmdFunc:              func(o gitops.PushOpts) *exec.Cmd { return exec.Command("true", "push", o.Remote) }, //nolint:gosec // test stub
		PullCmdFunc:              func(o gitops.PullOpts) *exec.Cmd { return exec.Command("true", "pull", o.Remote) }, //nolint:gosec // test stub
		FetchCmdFunc:             func(bool, bool) *exec.Cmd { return exec.Command("true", "fetch") },
	}
}

// newTestCommit builds a sized commit model with the status already loaded.
func newTestCommit(t *testing.T, repo *mocks.RepoMock) *CommitModel {
	t.Helper()
	cfg := testCommitConfig()
	cfg.Repo = repo
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	c.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return c
}

// runCmd executes a tea.Cmd synchronously and feeds the result back.
func runCmd(t *testing.T, c *CommitModel, cmd tea.Cmd) {
	t.Helper()
	require.NotNil(t, cmd)
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub != nil {
				c.Update(sub())
			}
		}
		return
	}
	c.Update(msg)
}

// loadAll runs the status load and the follow-up diff load.
func loadAll(t *testing.T, c *CommitModel) {
	t.Helper()
	runCmd(t, c, c.Init())
	if cmd := c.loadDiffForCursor(); cmd != nil {
		runCmd(t, c, cmd)
	}
}

func TestNewCommitModel_requiresRepo(t *testing.T) {
	_, err := NewCommitModel(CommitConfig{})
	require.Error(t, err)
	cfg := testCommitConfig()
	cfg.Repo = nil
	_, err = NewCommitModel(*cfg)
	require.Error(t, err)
	cfg = testCommitConfig()
	cfg.DiffPane = ModelConfig{}
	_, err = NewCommitModel(*cfg)
	require.Error(t, err, "diff pane config must be valid")
}

func TestNewCommitModel_defaults(t *testing.T) {
	cfg := testCommitConfig()
	cfg.Keymap = nil
	cfg.Overlay = nil
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	assert.Equal(t, keymap.ActionStageToggle, c.keymap.Resolve(" "))
	assert.False(t, c.overlay.Active())
	assert.Equal(t, 3, c.sideWidthRatio)
	assert.NotNil(t, c.run)
	assert.Equal(t, "loading...", c.View())
}

func TestCommitModel_StatusBuildsSections(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	rows := c.list.Rows()
	texts := make([]string, 0, len(rows))
	for _, r := range rows {
		texts = append(texts, r.Text)
	}
	assert.Equal(t, []string{"Staged (2)", "both.go", "staged.go", "Unstaged (3)", "a.go", "both.go", "new.txt"}, texts)
	fr, ok := c.cursorFile()
	require.True(t, ok)
	assert.Equal(t, "both.go", fr.file.Path)
	assert.True(t, fr.staged)

	view := c.View()
	assert.Contains(t, view, "Files")
	assert.Contains(t, view, "Staged (2)")
	assert.Contains(t, view, "main ↑1 ↓0 · origin/main · 2 stash")
	assert.Contains(t, view, "TWO", "diff pane shows the loaded diff")
	assert.Equal(t, "both.go", c.staging.spec.Path)
	assert.True(t, c.staging.spec.Cached, "staged row loads the index diff")
}

func TestCommitModel_StaleStatusAndDiffIgnored(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.Update(commitStatusMsg{seq: c.statusSeq - 1, status: gitops.Status{}})
	assert.Len(t, c.list.Rows(), 7, "stale status dropped")
	c.Update(commitDiffMsg{seq: c.staging.seq - 1, spec: gitops.DiffSpec{Path: "x"}, raw: ""})
	assert.Equal(t, "both.go", c.staging.spec.Path, "stale diff dropped")
}

func TestCommitModel_StatusErrorOpensPopup(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	repo.StatusFunc = func(context.Context) (gitops.Status, error) {
		return gitops.Status{}, &git.Error{Args: []string{"status"}, Stderr: "fatal: not a git repository\n", ExitCode: 128}
	}
	c := newTestCommit(t, repo)
	runCmd(t, c, c.Init())
	assert.True(t, c.overlay.Active())
	assert.Equal(t, overlay.KindError, c.overlay.Kind())
	assert.Contains(t, c.View(), "not a git repository")
	assert.True(t, c.inputBusy())
}

func TestCommitModel_MoveAndLoadDiff(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	cmd := c.Update(keyRunes("j"))
	runCmd(t, c, cmd)
	assert.Equal(t, "staged.go", c.staging.spec.Path)
	// moving onto the Unstaged section's first file switches to the worktree diff
	runCmd(t, c, c.Update(keyRunes("j")))
	assert.Equal(t, "a.go", c.staging.spec.Path)
	assert.False(t, c.staging.spec.Cached)
	runCmd(t, c, c.Update(tea.KeyMsg{Type: tea.KeyEnd}))
	assert.Equal(t, "new.txt", c.staging.spec.Path)
	assert.True(t, c.staging.spec.Untracked)
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyEnd}), "no move, no reload")
	calls := repo.StagingDiffCalls()
	assert.Equal(t, gitops.DiffSpec{Path: "new.txt", Untracked: true}, calls[len(calls)-1].Spec)
}

func TestCommitModel_StageToggle(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	c.repo = repo
	loadAll(t, c)

	// cursor on staged both.go -> unstage
	cmd := c.Update(keyRunes(" "))
	require.NotNil(t, cmd)
	msg := cmd()
	done, ok := msg.(commitOpDoneMsg)
	require.True(t, ok)
	require.NoError(t, done.err)
	require.Len(t, repo.UnstageFilesCalls(), 1)
	assert.Equal(t, []string{"both.go"}, repo.UnstageFilesCalls()[0].Paths)
	refresh := c.Update(msg)
	assert.NotNil(t, refresh, "successful op refreshes the status")

	// unstaged row -> stage
	c.list.SelectByKey("u:new.txt")
	cmd = c.Update(keyRunes(" "))
	cmd()
	require.Len(t, repo.StageFilesCalls(), 1)
	assert.Equal(t, []string{"new.txt"}, repo.StageFilesCalls()[0].Paths)

	// a / u operate on everything
	c.Update(keyRunes("a"))()
	c.Update(keyRunes("u"))()
	assert.Len(t, repo.StageAllCalls(), 1)
	assert.Len(t, repo.UnstageAllCalls(), 1)
}

func TestCommitModel_OpErrorOpensPopupAndRefreshes(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	repo.StageAllFunc = func(context.Context) error {
		return &git.Error{Args: []string{"add", "-A"}, Stderr: "fatal: boom\n", ExitCode: 128}
	}
	c := newTestCommit(t, repo)
	loadAll(t, c)
	cmd := c.Update(keyRunes("a"))
	refresh := c.Update(cmd())
	assert.NotNil(t, refresh)
	assert.Equal(t, overlay.KindError, c.overlay.Kind())
	assert.Contains(t, c.View(), "stage all failed")

	// a selection with no changes is only a hint
	c.overlay.Close()
	c.Update(commitOpDoneMsg{name: "stage lines", err: gitops.ErrNoChangesSelected})
	assert.False(t, c.overlay.Active())
	assert.Contains(t, c.statusBarText(), "nothing to apply")
}

func TestCommitModel_DiscardFlow(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	c.list.SelectByKey("u:both.go")

	// d opens the menu with both scopes
	assert.Nil(t, c.Update(keyRunes("d")))
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	view := c.View()
	assert.Contains(t, view, "discard unstaged changes")
	assert.Contains(t, view, "discard all changes")

	// choose "all" -> confirmation
	assert.Nil(t, c.Update(keyRunes("a")))
	require.Equal(t, overlay.KindConfirm, c.overlay.Kind())
	assert.Contains(t, c.View(), "index and worktree")

	// n cancels without touching the repo
	c.Update(keyRunes("n"))
	assert.False(t, c.overlay.Active())
	assert.Empty(t, repo.DiscardCalls())
	assert.Empty(t, c.pending.file.Path)

	// again, and confirm with y
	c.Update(keyRunes("d"))
	c.Update(keyRunes("u"))
	cmd := c.Update(keyRunes("y"))
	require.NotNil(t, cmd)
	cmd()
	require.Len(t, repo.DiscardCalls(), 1)
	assert.Equal(t, "both.go", repo.DiscardCalls()[0].F.Path)
	assert.Equal(t, gitops.DiscardUnstaged, repo.DiscardCalls()[0].Scope)

	// untracked file offers deletion only
	c.list.SelectByKey("u:new.txt")
	c.Update(keyRunes("d"))
	assert.Contains(t, c.View(), "delete untracked file")
	c.Update(keyRunes("d"))
	assert.Contains(t, c.View(), "Delete the untracked file")
	c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, c.overlay.Active())
}

func TestCommitModel_ToggleStagedView(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	// cursor on both.go (staged section): I shows the unstaged side
	runCmd(t, c, c.Update(keyRunes("I")))
	assert.Equal(t, "both.go", c.staging.spec.Path)
	assert.False(t, c.staging.spec.Cached)
	runCmd(t, c, c.Update(keyRunes("I")))
	assert.True(t, c.staging.spec.Cached)

	// staged.go has no unstaged side
	c.list.SelectByKey("s:staged.go")
	runCmd(t, c, c.loadDiffForCursor())
	assert.Nil(t, c.Update(keyRunes("I")))
	assert.Contains(t, c.statusBarText(), "one side only")

	// untracked files have no staged side
	c.list.SelectByKey("u:new.txt")
	assert.Nil(t, c.Update(keyRunes("I")))
}

func TestCommitModel_DiffFocusNavigation(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	assert.Equal(t, focusDiff, c.focus)
	assert.Contains(t, c.statusBarText(), "staged")
	before := c.diff.paneCursor()
	c.Update(keyRunes("j"))
	assert.Equal(t, before+1, c.diff.paneCursor())
	c.Update(keyRunes("]"))
	c.Update(keyRunes("["))
	c.Update(keyRunes("w")) // toggles wrap on the embedded pane
	assert.True(t, c.diff.modes.wrap)
	c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, focusSide, c.focus)
	c.Update(keyRunes("l"))
	assert.Equal(t, focusDiff, c.focus)
	c.Update(keyRunes("h"))
	assert.Equal(t, focusSide, c.focus)

	// with an empty diff the pane cannot take focus
	c.installRaw(gitops.DiffSpec{Path: "x"}, "")
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	assert.Equal(t, focusSide, c.focus)
	assert.Contains(t, c.statusBarText(), "no diff")
}

func TestCommitModel_EmptyStatus(t *testing.T) {
	c := newTestCommit(t, newRepoMock(gitops.Status{Head: gitops.BranchHead{Name: "main", OID: "abc"}}))
	runCmd(t, c, c.Init())
	assert.Nil(t, c.loadDiffForCursor())
	assert.Contains(t, c.View(), "working tree clean")
	assert.Nil(t, c.Update(keyRunes(" ")), "nothing to stage")
	assert.Nil(t, c.Update(keyRunes("d")))
	assert.False(t, c.overlay.Active())
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyTab}))
}

func TestCommitModel_HeadSummaryVariants(t *testing.T) {
	c := newTestCommit(t, newRepoMock(gitops.Status{}))
	assert.Equal(t, "…", c.headSummary())
	c.statusLoaded = true
	c.status.Head = gitops.BranchHead{Detached: true, OID: "0123456789"}
	assert.Equal(t, "HEAD detached at 0123456", c.headSummary())
	c.status.Head = gitops.BranchHead{Name: "main", Unborn: true}
	assert.Equal(t, "main (no commits)", c.headSummary())
	c.status.Head = gitops.BranchHead{Name: "feat", Upstream: "origin/feat", UpstreamGone: true}
	assert.Equal(t, "feat · origin/feat (gone)", c.headSummary())
}

func TestCommitModel_EditorFlow(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		launched = cmd
		return func() tea.Msg { return done(nil) }
	}
	c.editor = &mocks.ExternalEditorMock{
		SourceCommandFunc: func(path string, line int) (*exec.Cmd, error) {
			return exec.Command("true", path, "+"+strconv.Itoa(line)), nil //nolint:gosec // test stub, fixed program
		},
	}
	c.repoRoot = "/repo"
	cmd := c.Update(keyRunes("e"))
	require.NotNil(t, cmd)
	require.NotNil(t, launched)
	assert.Equal(t, []string{"true", "/repo/both.go", "+1"}, launched.Args)
	refresh := c.Update(cmd())
	assert.NotNil(t, refresh, "returning from the editor refreshes the status")

	c.editor = nil
	assert.Nil(t, c.Update(keyRunes("e")))
	assert.Contains(t, c.statusBarText(), "no editor")

	c.editor = &mocks.ExternalEditorMock{SourceCommandFunc: func(string, int) (*exec.Cmd, error) { return nil, errors.New("nope") }}
	assert.Nil(t, c.Update(keyRunes("e")))
	assert.Contains(t, c.statusBarText(), "cannot open editor")
}

func TestCommitModel_ProcessDoneRestoresMouse(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	cmd := c.Update(commitProcessDoneMsg{name: "editor", err: errors.New("exit 1"), restoreMouse: true})
	require.NotNil(t, cmd)
	assert.Contains(t, c.statusBarText(), "editor failed")
}

func TestRawToDiffLines(t *testing.T) {
	lines, mapping, hunks, binary := rawToDiffLines(sampleRaw)
	require.Len(t, lines, 5)
	assert.Equal(t, []int{-1, 5, 6, 7, 8}, mapping)
	assert.Equal(t, 1, hunks)
	assert.False(t, binary)
	assert.Equal(t, "@@ -1,3 +1,3 @@", lines[0].Content)
	assert.Equal(t, 1, lines[1].OldNum)
	assert.Equal(t, 2, lines[2].OldNum)
	assert.Equal(t, 0, lines[2].NewNum)
	assert.Equal(t, "TWO", lines[3].Content)
	assert.Equal(t, 2, lines[3].NewNum)
	assert.Equal(t, 3, lines[4].NewNum)

	lines, mapping, hunks, binary = rawToDiffLines("diff --git a/x b/x\nBinary files a/x and b/x differ\n")
	require.Len(t, lines, 1)
	assert.True(t, binary)
	assert.True(t, lines[0].IsBinary)
	assert.Equal(t, []int{-1}, mapping)
	assert.Equal(t, 0, hunks)

	lines, _, _, _ = rawToDiffLines("")
	assert.Nil(t, lines)
	lines, _, _, _ = rawToDiffLines("diff --git a/x b/x\n")
	assert.Nil(t, lines, "header-only diff has nothing to show")
	lines, _, _, _ = rawToDiffLines("@@ broken\n+x\n")
	require.Len(t, lines, 1)
	assert.True(t, lines[0].IsPlaceholder)
}

func TestCommitModel_HelpAndQuit(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	c.Update(keyRunes("?"))
	assert.True(t, c.overlay.Active())
	assert.Contains(t, c.View(), "Navigation")
	c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, c.overlay.Active())
	cmd := c.Update(keyRunes("q"))
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	assert.Nil(t, c.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}))
	c.Update(keyRunes("3"))
	assert.Equal(t, tabLog, c.tab)
}

func TestCommitModel_SetStyleRefreshesDiff(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	loadAll(t, c)
	c.setStyle(c.styleGen, c.diff.resolver, c.diff.renderer, c.diff.sgr, nil) // same generation: no-op
	c.setStyle(c.styleGen+1, c.diff.resolver, c.diff.renderer, c.diff.sgr, noopHighlighter())
	assert.Contains(t, c.View(), "TWO")
}

func TestNewCommitModel_reviewOnlyFeaturesOff(t *testing.T) {
	cfg := testCommitConfig()
	cfg.Repo = newRepoMock(sampleStatus())
	cfg.DiffPane.ShowBlame = true
	cfg.DiffPane.Collapsed = true
	cfg.DiffPane.Compact = true
	cfg.DiffPane.Wrap = true
	cfg.DiffPane.LineNumbers = true
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	assert.False(t, c.diff.modes.showBlame)
	assert.False(t, c.diff.modes.collapsed.enabled)
	assert.False(t, c.diff.modes.compact)
	assert.True(t, c.diff.modes.wrap, "display settings are shared")
	assert.True(t, c.diff.modes.lineNumbers)
	assert.True(t, c.diff.host.embedded)
	assert.Same(t, cfg.Overlay, c.diff.overlay, "one overlay manager per mode")
}
