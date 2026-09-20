package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yousysadmin/igit/internal/git"
)

// setupRepo creates an isolated git repository with one committed file and
// returns its Git handle. Global and system git config are disabled so the
// developer's settings (gpg signing, hooks, autocrlf) cannot leak in.
func setupRepo(t *testing.T) *Git {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	gitRun(t, dir, "config", "core.autocrlf", "false")
	writeRepoFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	gitRun(t, dir, "add", "a.txt")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return New(dir)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test helper with fixed arguments
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func TestSupports(t *testing.T) {
	g := setupRepo(t)
	assert.True(t, g.Supports(t.Context(), 2, 11))
	assert.False(t, g.Supports(t.Context(), 99, 0))
	bad := New(filepath.Join(t.TempDir(), "missing"))
	assert.False(t, bad.Supports(t.Context(), 1, 0), "a failed probe reports no support")
}

func TestStatus_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()

	st, err := g.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, "main", st.Head.Name)
	assert.Len(t, st.Head.OID, 40)
	assert.Empty(t, st.Files)

	// commit a file that will be renamed, then build up modified, staged+modified,
	// untracked and renamed entries
	writeRepoFile(t, dir, "sub dir/d.txt", "d\n")
	gitRun(t, dir, "add", "sub dir/d.txt")
	gitRun(t, dir, "commit", "-q", "-m", "add d")
	gitRun(t, dir, "mv", "sub dir/d.txt", "sub dir/renamed.txt")
	writeRepoFile(t, dir, "a.txt", "one\nTWO\nthree\n")
	writeRepoFile(t, dir, "b.txt", "b\n")
	gitRun(t, dir, "add", "b.txt")
	writeRepoFile(t, dir, "b.txt", "b\nmore\n")
	writeRepoFile(t, dir, "c.txt", "c\n")

	st, err = g.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"b.txt", "sub dir/renamed.txt"}, paths(st.Staged()))
	assert.Equal(t, []string{"a.txt", "b.txt", "c.txt"}, paths(st.Unstaged()))
	byPath := map[string]StatusEntry{}
	for _, f := range st.Files {
		byPath[f.Path] = f
	}
	assert.Equal(t, "AM", byPath["b.txt"].ShortStatus())
	assert.True(t, byPath["b.txt"].IsNewInIndex())
	assert.True(t, byPath["c.txt"].Untracked)
	renamed := byPath["sub dir/renamed.txt"]
	assert.Equal(t, "sub dir/d.txt", renamed.OrigPath)
	assert.Equal(t, ChangeCode('R'), renamed.Index)
	assert.Equal(t, 100, renamed.RenameScore)

	// stash count shows up
	gitRun(t, dir, "stash", "push", "-q", "-m", "wip", "--", "a.txt")
	st, err = g.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, st.StashCount)
}

func TestStatus_e2e_conflictAndDetached(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()

	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeRepoFile(t, dir, "a.txt", "one\nfeature\nthree\n")
	gitRun(t, dir, "commit", "-q", "-am", "feature")
	gitRun(t, dir, "checkout", "-q", "main")
	writeRepoFile(t, dir, "a.txt", "one\nmain\nthree\n")
	gitRun(t, dir, "commit", "-q", "-am", "main")
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = dir
	_ = cmd.Run() // exits non-zero because of the conflict

	st, err := g.Status(ctx)
	require.NoError(t, err)
	require.Len(t, st.Conflicted(), 1)
	assert.Equal(t, "UU", st.Conflicted()[0].ShortStatus())
	assert.Empty(t, st.Unstaged())

	gitRun(t, dir, "merge", "--abort")
	gitRun(t, dir, "checkout", "-q", "--detach")
	st, err = g.Status(ctx)
	require.NoError(t, err)
	assert.True(t, st.Head.Detached)
	assert.Empty(t, st.Head.Name)
}

func TestStatus_e2e_unbornAndUpstream(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitRun(t, dir, "init", "-q", "-b", "main")
	g := New(dir)
	st, err := g.Status(t.Context())
	require.NoError(t, err)
	assert.True(t, st.Head.Unborn)
	assert.Equal(t, "main", st.Head.Name)

	// upstream tracking: clone into a second repo and add a commit there
	writeRepoFile(t, dir, "a.txt", "a\n")
	gitRun(t, dir, "config", "user.email", "t@e.com")
	gitRun(t, dir, "config", "user.name", "T")
	gitRun(t, dir, "add", "a.txt")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	clone := filepath.Join(t.TempDir(), "clone")
	gitRun(t, filepath.Dir(clone), "clone", "-q", dir, clone)
	gitRun(t, clone, "config", "user.email", "t@e.com")
	gitRun(t, clone, "config", "user.name", "T")
	writeRepoFile(t, clone, "b.txt", "b\n")
	gitRun(t, clone, "add", "b.txt")
	gitRun(t, clone, "commit", "-q", "-m", "ahead")
	st, err = New(clone).Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "origin/main", st.Head.Upstream)
	assert.Equal(t, 1, st.Head.Ahead)
	assert.Equal(t, 0, st.Head.Behind)
	assert.False(t, st.Head.UpstreamGone)
}

func TestStatus_notARepo(t *testing.T) {
	g := New(t.TempDir())
	_, err := g.Status(t.Context())
	require.Error(t, err)
	var gerr *git.Error
	assert.ErrorAs(t, err, &gerr)
}
