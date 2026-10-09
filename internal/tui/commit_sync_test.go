package tui

import (
	"errors"
	"os/exec"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

func TestCommitModel_SyncMirrorsBothStreams(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)

	press(t, c, "P")
	press(t, c, "p")
	require.NotNil(t, launched)
	assert.NotNil(t, launched.Stdout, "a pre-push hook reports on stdout")
	assert.NotNil(t, launched.Stderr)

	// a pre-push hook's stdout reaches the popup
	c.run = func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			_, _ = cmd.Stdout.Write([]byte("eslint: 3 problems\n"))
			_, _ = cmd.Stderr.Write([]byte("error: failed to push some refs\n"))
			return done(errors.New("exit status 1"))
		}
	}
	press(t, c, "P")
	press(t, c, "p")
	require.Equal(t, overlay.KindError, c.overlay.Kind())
	assert.Contains(t, c.View(), "eslint: 3 problems")
	assert.Contains(t, c.View(), "failed to push some refs")
	press(t, c, "esc")

	// a silent failure still says so
	c.run = func(_ *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg { return done(errors.New("exit status 1")) }
	}
	press(t, c, "f")
	require.Equal(t, overlay.KindError, c.overlay.Kind())
	assert.Contains(t, c.View(), "no output")
	press(t, c, "esc")
}

func TestCommitModel_ContinueKeepsStdoutForTheEditor(t *testing.T) {
	st := sampleStatus()
	st.InProgress = gitops.InProgress{State: gitops.StateRebasing}
	c := newTestCommit(t, newRepoMock(st))
	loadAll(t, c)
	var launched *exec.Cmd
	c.run = captureRunner(&launched)
	cmd, ok := c.handleInProgressMenu("op-continue")
	require.True(t, ok)
	require.NotNil(t, cmd)
	require.NotNil(t, launched)
	assert.Nil(t, launched.Stdout)
	assert.NotNil(t, launched.Stderr)
}

func TestCommitModel_SyncRefusedWhileBusy(t *testing.T) {
	repo := newRepoMock(sampleStatus())
	c := newTestCommit(t, repo)
	loadAll(t, c)
	var done func(error) tea.Msg
	c.run = func(_ *exec.Cmd, d func(error) tea.Msg) tea.Cmd {
		done = d
		return nil
	}
	c.Update(keyRunes("f"))
	assert.Equal(t, "fetch", c.busy)
	assert.Contains(t, c.statusBarText(), "fetch running")

	c.Update(keyRunes("f"))
	assert.Contains(t, c.statusBarText(), "fetch in progress")
	require.Len(t, repo.FetchCmdCalls(), 1)

	c.Update(done(nil))
	assert.Empty(t, c.busy)
	assert.Contains(t, c.statusBarText(), "fetch done")
}

func TestSyncBuffer(t *testing.T) {
	var b syncBuffer
	_, _ = b.Write([]byte("out\n"))
	_, _ = b.Write([]byte("err\n"))
	assert.Equal(t, "out\nerr\n", b.String())
}
