package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommit_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()

	has, err := g.HasStagedChanges(ctx)
	require.NoError(t, err)
	assert.False(t, has)

	writeRepoFile(t, dir, "a.txt", "one\nTWO\nthree\n")
	require.NoError(t, g.StageFiles(ctx, []string{"a.txt"}))
	has, err = g.HasStagedChanges(ctx)
	require.NoError(t, err)
	assert.True(t, has)

	msg := "feat: change two\n\nBody line with ünïcode\n\n## not a header\n"
	require.NoError(t, g.Commit(ctx, msg, CommitOpts{}))
	last, err := g.LastCommitMessage(ctx)
	require.NoError(t, err)
	assert.Equal(t, "feat: change two\n\nBody line with ünïcode\n\n## not a header", last, "--cleanup=whitespace keeps # lines")

	// amend without editing keeps the message
	writeRepoFile(t, dir, "b.txt", "b\n")
	require.NoError(t, g.StageFiles(ctx, []string{"b.txt"}))
	require.NoError(t, g.Commit(ctx, "", CommitOpts{Amend: true, NoEdit: true}))
	assert.Equal(t, "1", strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD^..HEAD")))
	assert.Contains(t, gitOut(t, dir, "show", "--stat", "--format=", "HEAD"), "b.txt")
	last, err = g.LastCommitMessage(ctx)
	require.NoError(t, err)
	assert.Equal(t, "feat: change two\n\nBody line with ünïcode\n\n## not a header", last)

	// empty message without amend is refused before git runs
	require.Error(t, g.Commit(ctx, "", CommitOpts{}))

	// nothing staged: git refuses unless AllowEmpty
	require.Error(t, g.Commit(ctx, "empty", CommitOpts{}))
	require.NoError(t, g.Commit(ctx, "empty", CommitOpts{AllowEmpty: true, Signoff: true}))
	last, err = g.LastCommitMessage(ctx)
	require.NoError(t, err)
	assert.Contains(t, last, "Signed-off-by: Test <test@example.com>")

	msgs, err := g.CommitMessages(ctx, 10)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Contains(t, msgs[0], "empty")
	assert.Equal(t, "feat: change two\n\nBody line with ünïcode\n\n## not a header", msgs[1])
	assert.Equal(t, "init", msgs[2])
	msgs, err = g.CommitMessages(ctx, 0)
	require.NoError(t, err)
	assert.Nil(t, msgs)
}

func TestCommit_hooksAndNoVerify_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700)) //nolint:gosec // hook must be executable
	writeRepoFile(t, dir, "a.txt", "changed\n")
	require.NoError(t, g.StageFiles(ctx, []string{"a.txt"}))
	err := g.Commit(ctx, "blocked", CommitOpts{})
	require.Error(t, err, "pre-commit hook rejects")
	require.NoError(t, g.Commit(ctx, "forced", CommitOpts{NoVerify: true}))
}

func TestCommitEditorCmd(t *testing.T) {
	g := New(t.TempDir())
	cmd, cleanup, err := g.CommitEditorCmd(CommitOpts{Amend: true, NoVerify: true}, "")
	require.NoError(t, err)
	cleanup()
	assert.Equal(t, []string{"git", "commit", "--amend", "--no-verify"}, cmd.Args)
	assert.Equal(t, g.WorkDir(), cmd.Dir)
	assert.NotContains(t, cmd.Env, "GIT_TERMINAL_PROMPT=0", "the editor commit is interactive")

	cmd, cleanup, err = g.CommitEditorCmd(CommitOpts{}, "seed subject\n\nbody\n")
	require.NoError(t, err)
	require.Len(t, cmd.Args, 4)
	assert.Equal(t, "--edit", cmd.Args[2])
	file := strings.TrimPrefix(cmd.Args[3], "--file=")
	data, err := os.ReadFile(file) //nolint:gosec // temp file created by the code under test
	require.NoError(t, err)
	assert.Equal(t, "seed subject\n\nbody\n", string(data))
	cleanup()
	assert.NoFileExists(t, file)
	cleanup() // idempotent
}

func TestCommit_unbornBranch_e2e(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "t@e.com")
	gitRun(t, dir, "config", "user.name", "T")
	g := New(dir)
	ctx := t.Context()

	last, err := g.LastCommitMessage(ctx)
	require.NoError(t, err)
	assert.Empty(t, last)
	msgs, err := g.CommitMessages(ctx, 5)
	require.NoError(t, err)
	assert.Empty(t, msgs)
	has, err := g.HasStagedChanges(ctx)
	require.NoError(t, err)
	assert.False(t, has)

	writeRepoFile(t, dir, "a.txt", "a\n")
	require.NoError(t, g.StageFiles(ctx, []string{"a.txt"}))
	has, err = g.HasStagedChanges(ctx)
	require.NoError(t, err)
	assert.True(t, has, "an unborn branch counts index entries as staged")
	require.NoError(t, g.Commit(ctx, "first", CommitOpts{}))
	last, err = g.LastCommitMessage(ctx)
	require.NoError(t, err)
	assert.Equal(t, "first", last)
}

func TestGPGSignEnabled(t *testing.T) {
	g := setupRepo(t)
	assert.False(t, g.GPGSignEnabled(t.Context()))
	gitRun(t, g.WorkDir(), "config", "commit.gpgsign", "true")
	assert.True(t, g.GPGSignEnabled(t.Context()))
	assert.False(t, New(t.TempDir()).GPGSignEnabled(t.Context()))
}

func TestCommitOpts_args(t *testing.T) {
	assert.Equal(t, []string{"commit"}, CommitOpts{}.args())
	assert.Equal(t, []string{"commit", "--amend", "--no-edit", "--no-verify", "--signoff", "--allow-empty"},
		CommitOpts{Amend: true, NoEdit: true, NoVerify: true, Signoff: true, AllowEmpty: true}.args())
}
