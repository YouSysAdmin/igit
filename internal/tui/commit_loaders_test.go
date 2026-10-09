package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

func TestCommitModel_ReportProcessFailure(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	exit1 := errors.New("exit status 1")

	c.Update(commitProcessDoneMsg{name: "editor", err: exit1})
	assert.False(t, c.overlay.Active())
	assert.Contains(t, c.statusBarText(), "editor failed: exit status 1")

	c.Update(commitProcessDoneMsg{name: "amend", err: exit1, output: "Aborting commit due to empty commit message.\n"})
	assert.False(t, c.overlay.Active())
	assert.Contains(t, c.statusBarText(), "amend aborted: empty message")

	c.Update(commitProcessDoneMsg{name: "commit", err: exit1})
	require.Equal(t, overlay.KindError, c.overlay.Kind())
	assert.Contains(t, c.View(), "a hook may have rejected the commit")
	c.overlay.Close()

	c.Update(commitProcessDoneMsg{name: "commit", err: exit1, output: "commit-msg: subject too long\n"})
	require.Equal(t, overlay.KindError, c.overlay.Kind())
	assert.Contains(t, c.View(), "commit-msg: subject too long")
}

func TestCommitModel_ShowErrorNamesSilentHook(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	silent := &git.Error{Args: []string{"commit", "-F", "-"}, ExitCode: 1, Err: errors.New("exit status 1")}
	c.showError("commit failed", silent)
	assert.Contains(t, c.View(), "a hook may have rejected the commit")
	c.overlay.Close()

	c.showError("stage failed", silent)
	assert.NotContains(t, c.View(), "a hook may have rejected")
	c.overlay.Close()

	loud := &git.Error{Args: []string{"commit"}, Stderr: "pre-commit: nope\n", ExitCode: 1}
	c.showError("commit failed", loud)
	assert.Contains(t, c.View(), "pre-commit: nope")
	assert.NotContains(t, c.View(), "a hook may have rejected")
}

func TestCommitModel_RunOpSerializesWrites(t *testing.T) {
	c := newTestCommit(t, newRepoMock(sampleStatus()))
	first := c.runOp("stage", func(context.Context) error { return nil })
	require.NotNil(t, first)
	assert.Equal(t, "stage", c.busy)
	assert.Nil(t, c.runOp("unstage", func(context.Context) error { return nil }))
	assert.Contains(t, c.statusBarText(), "stage in progress")

	c.hint = "stage running"
	c.Update(first())
	assert.Empty(t, c.busy)
	assert.Empty(t, c.hint, "the progress hint goes with the operation")
	assert.NotNil(t, c.runOp("unstage", func(context.Context) error { return nil }))
}
