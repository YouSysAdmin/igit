package tui

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// captureRunner records the interactive command and runs its done callback
// synchronously when the returned cmd executes.
func captureRunner(launched **exec.Cmd) processRunner {
	return func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		*launched = cmd
		return func() tea.Msg { return done(nil) }
	}
}

func TestCommitModel_CommitPromptFlow(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)

	// c gathers prompt data asynchronously, then opens the prompt
	cmd := c.Update(keyRunes("c"))
	require.NotNil(t, cmd)
	c.Update(cmd())
	require.Equal(t, overlay.KindPrompt, c.overlay.Kind())
	assert.True(t, c.inputBusy())
	assert.Contains(t, c.View(), "commit message")

	// history recall and typing
	c.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, "last subject", c.overlay.(*overlay.Manager).PromptValue())
	c.Update(tea.KeyMsg{Type: tea.KeyDown})
	c.Update(keyRunes("feat: thing"))
	cmd = c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.False(t, c.overlay.Active())
	require.Len(t, repo.CommitFileCmdCalls(), 1)
	assert.Equal(t, "feat: thing", repo.CommitFileCmdCalls()[0].Msg)
	assert.Equal(t, gitops.CommitOpts{}, repo.CommitFileCmdCalls()[0].O)
	assert.Equal(t, []string{"true", "file", "feat: thing"}, launched.Args, "the commit runs in the terminal")
	refresh := c.Update(cmd())
	assert.NotNil(t, refresh)
	assert.Equal(t, "commit done", c.statusBarText())
}

func TestCommitModel_CommitNothingStaged(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	repo.HasStagedChangesFunc = func(context.Context) (bool, error) { return false, nil }
	c := newTestCommit(t, repo)
	loadAll(t, c)
	c.Update(c.Update(keyRunes("c"))())
	assert.False(t, c.overlay.Active())
	assert.Contains(t, c.statusBarText(), "nothing staged")

	repo.HasStagedChangesFunc = func(context.Context) (bool, error) { return false, errors.New("boom") }
	c.Update(c.Update(keyRunes("c"))())
	assert.Equal(t, overlay.KindError, c.overlay.Kind())
}

func TestCommitModel_CommitPromptToEditor(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)
	c.Update(c.Update(keyRunes("c"))())
	c.Update(keyRunes("wip"))
	cmd := c.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	require.NotNil(t, cmd)
	require.Len(t, repo.CommitEditorCmdCalls(), 1)
	assert.Equal(t, "wip", repo.CommitEditorCmdCalls()[0].Seed)
	assert.Equal(t, []string{"true", "editor", "wip"}, launched.Args)
	refresh := c.Update(cmd())
	assert.NotNil(t, refresh, "returning from the editor refreshes")
	assert.Empty(t, repo.CommitFileCmdCalls())
}

func TestCommitModel_CommitEditorDirect(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)
	cmd := c.Update(keyRunes("C"))
	require.NotNil(t, cmd)
	require.Len(t, repo.CommitEditorCmdCalls(), 1)
	assert.Empty(t, repo.CommitEditorCmdCalls()[0].Seed)
	assert.False(t, repo.CommitEditorCmdCalls()[0].O.Amend)
	c.Update(cmd())

	repo.CommitEditorCmdFunc = func(gitops.CommitOpts, string) (*exec.Cmd, func(), error) {
		return nil, func() {}, errors.New("no tmp")
	}
	assert.Nil(t, c.Update(keyRunes("C")))
	assert.Contains(t, c.statusBarText(), "no tmp")
}

func TestCommitModel_CommitFileCmdError(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	repo.CommitFileCmdFunc = func(gitops.CommitOpts, string) (*exec.Cmd, func(), error) {
		return nil, func() {}, errors.New("tmp fail")
	}
	c.Update(c.Update(keyRunes("c"))())
	c.Update(keyRunes("x"))
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	assert.Contains(t, c.statusBarText(), "tmp fail")
}

func TestCommitModel_AmendKeepUsesConfiguredOpts(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	c.noVerify = true
	var launched *exec.Cmd
	c.run = captureRunner(&launched)
	c.Update(keyRunes("A"))
	c.Update(keyRunes("k"))
	cmd := c.Update(keyRunes("y"))
	require.NotNil(t, cmd)
	require.Len(t, repo.CommitEditorCmdCalls(), 1)
	call := repo.CommitEditorCmdCalls()[0]
	assert.Empty(t, call.Seed)
	assert.Equal(t, gitops.CommitOpts{Amend: true, NoEdit: true, NoVerify: true}, call.O)
	require.NotNil(t, launched)
	c.Update(cmd())
	assert.Equal(t, "amend done", c.statusBarText())
}

func TestCommitModel_AmendMenu(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)

	// keep message: menu -> confirm -> commit --amend --no-edit
	assert.Nil(t, c.Update(keyRunes("A")))
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	assert.Nil(t, c.Update(keyRunes("k")))
	require.Equal(t, overlay.KindConfirm, c.overlay.Kind())
	cmd := c.Update(keyRunes("y"))
	require.NotNil(t, cmd)
	require.Len(t, repo.CommitEditorCmdCalls(), 1)
	assert.Empty(t, repo.CommitEditorCmdCalls()[0].Seed)
	assert.Equal(t, gitops.CommitOpts{Amend: true, NoEdit: true}, repo.CommitEditorCmdCalls()[0].O)
	c.Update(cmd())
	assert.Equal(t, "amend done", c.statusBarText())

	// edit message: prompt prefilled with the old subject, body preserved on submit
	c.Update(keyRunes("A"))
	cmd = c.Update(keyRunes("e"))
	require.NotNil(t, cmd)
	c.Update(cmd())
	require.Equal(t, overlay.KindPrompt, c.overlay.Kind())
	assert.Equal(t, "last subject", c.overlay.(*overlay.Manager).PromptValue())
	c.Update(keyRunes(" v2"))
	cmd = c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	c.Update(cmd())
	require.Len(t, repo.CommitFileCmdCalls(), 1)
	assert.Equal(t, "last subject v2\n\nlast body", repo.CommitFileCmdCalls()[0].Msg)
	assert.True(t, repo.CommitFileCmdCalls()[0].O.Amend)
	assert.False(t, repo.CommitFileCmdCalls()[0].O.NoEdit)

	// amend prompt -> editor carries subject and body as the seed
	c.Update(keyRunes("A"))
	c.Update(c.Update(keyRunes("e"))())
	cmd = c.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	require.NotNil(t, cmd)
	calls := repo.CommitEditorCmdCalls()
	require.Len(t, calls, 2)
	calls = calls[1:]
	assert.Equal(t, "last subject\n\nlast body", calls[0].Seed)
	assert.True(t, calls[0].O.Amend)
	c.Update(cmd())

	// unborn branch: nothing to amend
	c.status.Head.Unborn = true
	assert.Nil(t, c.Update(keyRunes("A")))
	assert.False(t, c.overlay.Active())
	assert.Contains(t, c.statusBarText(), "nothing to amend")
}

func TestCommitModel_CommitOptsFromConfig(t *testing.T) {
	cfg := testCommitConfig()
	cfg.NoVerify = true
	cfg.Signoff = true
	cfg.Repo = newRepoMock(sampleStatus())
	c, err := NewCommitModel(*cfg)
	require.NoError(t, err)
	assert.Equal(t, gitops.CommitOpts{NoVerify: true, Signoff: true}, c.commitOpts(false))
	assert.Equal(t, gitops.CommitOpts{Amend: true, NoVerify: true, Signoff: true}, c.commitOpts(true))
}

func TestCommitModel_PromptHelpers(t *testing.T) {
	assert.Equal(t, "a", firstLineOf("a\nb"))
	assert.Equal(t, "a", firstLineOf("a"))
	assert.Equal(t, "b\nc", bodyOf("a\n\nb\nc\n"))
	assert.Empty(t, bodyOf("a"))
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	assert.Nil(t, c.handlePromptSubmitted("unknown", "x"))
}

func TestCommitModel_CommitProcessMirrorsStderr(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)
	cmd := c.Update(keyRunes("C"))
	require.NotNil(t, cmd)
	require.NotNil(t, launched)
	assert.NotNil(t, launched.Stderr, "stderr is mirrored so a hook's complaint survives the screen switch")
	assert.Nil(t, launched.Stdout, "the editor keeps stdout as a tty")
	assert.Equal(t, "commit", c.busy)
	c.Update(cmd())
	assert.Empty(t, c.busy)
	assert.Contains(t, c.statusBarText(), "commit done")

	// a hook rejecting the editor commit: its output lands in the popup
	c.run = func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			_, _ = cmd.Stderr.Write([]byte("gofmt: a.go needs formatting\n"))
			return done(errors.New("exit status 1"))
		}
	}
	c.Update(c.Update(keyRunes("C"))())
	require.Equal(t, overlay.KindError, c.overlay.Kind())
	assert.Contains(t, c.View(), "commit failed")
	assert.Contains(t, c.View(), "gofmt: a.go needs formatting")
	c.overlay.Close()

	// leaving the editor with an empty message is an abort, not an error
	c.run = func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			_, _ = cmd.Stderr.Write([]byte("Aborting commit due to empty commit message.\n"))
			return done(errors.New("exit status 1"))
		}
	}
	c.Update(c.Update(keyRunes("C"))())
	assert.False(t, c.overlay.Active())
	assert.Contains(t, c.statusBarText(), "commit aborted: empty message")
}

func TestCommitModel_CommitRefusedWhileBusy(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)
	c.Update(c.Update(keyRunes("c"))())
	c.Update(keyRunes("slow hook"))
	cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, "commit", c.busy)

	// a second commit, a stage and an editor commit all wait for it
	assert.Nil(t, c.Update(keyRunes(" ")))
	assert.Contains(t, c.statusBarText(), "commit in progress")
	assert.Nil(t, c.Update(keyRunes("C")))
	assert.Empty(t, repo.CommitEditorCmdCalls())
	assert.Nil(t, c.Update(keyRunes("c")), "the prompt does not open while the commit runs")
	assert.False(t, c.overlay.Active())

	c.Update(cmd())
	assert.Empty(t, c.busy)
	assert.Contains(t, c.statusBarText(), "commit done")
	require.Len(t, repo.CommitFileCmdCalls(), 1)
}
