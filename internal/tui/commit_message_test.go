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
	done := cmd()
	require.Len(t, repo.CommitCalls(), 1)
	assert.Equal(t, "feat: thing", repo.CommitCalls()[0].Msg)
	assert.Equal(t, gitops.CommitOpts{}, repo.CommitCalls()[0].O)
	refresh := c.Update(done)
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
	assert.Empty(t, repo.CommitCalls())
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

	repo.CommitEditorCmdFunc = func(gitops.CommitOpts, string) (*exec.Cmd, func(), error) {
		return nil, func() {}, errors.New("no tmp")
	}
	assert.Nil(t, c.Update(keyRunes("C")))
	assert.Contains(t, c.statusBarText(), "no tmp")
}

func TestCommitModel_GPGCommitGoesThroughTerminal(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	repo.GPGSignEnabledFunc = func(context.Context) bool { return true }
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)
	c.Update(c.Update(keyRunes("c"))())
	c.Update(keyRunes("signed"))
	cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Len(t, repo.CommitFileCmdCalls(), 1)
	assert.Equal(t, "signed", repo.CommitFileCmdCalls()[0].Msg)
	assert.Empty(t, repo.CommitCalls())
	c.Update(cmd())

	repo.CommitFileCmdFunc = func(gitops.CommitOpts, string) (*exec.Cmd, func(), error) {
		return nil, func() {}, errors.New("tmp fail")
	}
	c.Update(c.Update(keyRunes("c"))())
	c.Update(keyRunes("x"))
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	assert.Contains(t, c.statusBarText(), "tmp fail")
}

func TestCommitModel_AmendMenu(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)

	// keep message: menu -> confirm -> commit --amend --no-edit
	assert.Nil(t, c.Update(keyRunes("A")))
	require.Equal(t, overlay.KindMenu, c.overlay.Kind())
	assert.Nil(t, c.Update(keyRunes("k")))
	require.Equal(t, overlay.KindConfirm, c.overlay.Kind())
	cmd := c.Update(keyRunes("y"))
	require.NotNil(t, cmd)
	done := cmd()
	require.Len(t, repo.CommitCalls(), 1)
	assert.Empty(t, repo.CommitCalls()[0].Msg)
	assert.Equal(t, gitops.CommitOpts{Amend: true, NoEdit: true}, repo.CommitCalls()[0].O)
	c.Update(done)
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
	cmd()
	require.Len(t, repo.CommitCalls(), 2)
	assert.Equal(t, "last subject v2\n\nlast body", repo.CommitCalls()[1].Msg)
	assert.True(t, repo.CommitCalls()[1].O.Amend)
	assert.False(t, repo.CommitCalls()[1].O.NoEdit)

	// amend prompt -> editor carries subject and body as the seed
	var launched *exec.Cmd
	c.run = captureRunner(&launched)
	c.Update(keyRunes("A"))
	c.Update(c.Update(keyRunes("e"))())
	cmd = c.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	require.NotNil(t, cmd)
	calls := repo.CommitEditorCmdCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "last subject\n\nlast body", calls[0].Seed)
	assert.True(t, calls[0].O.Amend)

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
