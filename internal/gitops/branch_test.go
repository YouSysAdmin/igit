package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/conflict"
	"github.com/yousysadmin/igit/internal/git"
)

func TestLocalBranchFor(t *testing.T) {
	remotes := []string{"origin", "upstream", "corp/mirror"}
	assert.Equal(t, "main", localBranchFor("origin/main", remotes))
	assert.Equal(t, "feature/one", localBranchFor("origin/feature/one", remotes), "the branch may hold slashes of its own")
	assert.Equal(t, "main", localBranchFor("upstream/main", remotes))
	assert.Equal(t, "main", localBranchFor("corp/mirror/main", remotes), "the longest remote wins over a shorter prefix")
	assert.Empty(t, localBranchFor("nowhere/main", remotes), "no remote owns it")
	assert.Empty(t, localBranchFor("origin", remotes), "a bare remote name is not a branch")
}

// setupClone gives a repository with a remote holding an extra branch.
func setupClone(t *testing.T) *Git {
	t.Helper()
	origin := setupRepo(t)
	gitRun(t, origin.WorkDir(), "branch", "feature")
	gitRun(t, origin.WorkDir(), "branch", "shared")

	dir := filepath.Join(t.TempDir(), "clone")
	gitRun(t, t.TempDir(), "clone", "-q", origin.WorkDir(), dir)
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	return New(dir)
}

func headRef(t *testing.T, g *Git) string {
	t.Helper()
	out, err := g.Run(t.Context(), git.RunOpts{Background: true, OkExitCodes: []int{1}}, "symbolic-ref", "-q", "--short", "HEAD")
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func TestGit_CheckoutRemote(t *testing.T) {
	g := setupClone(t)

	// picking a remote branch means working on it, not visiting its commit
	require.NoError(t, g.CheckoutRemote(t.Context(), "origin/feature", false))
	assert.Equal(t, "feature", headRef(t, g), "a plain checkout of origin/feature would detach HEAD")
	assert.True(t, g.hasLocalBranch(t.Context(), "feature"))

	branches, err := g.Branches(t.Context())
	require.NoError(t, err)
	var tracked string
	for _, b := range branches {
		if b.Name == "feature" && !b.Remote {
			tracked = b.Upstream
		}
	}
	assert.Equal(t, "origin/feature", tracked, "the new branch tracks the one it came from")
}

func TestGit_CheckoutRemote_existingLocalBranch(t *testing.T) {
	g := setupClone(t)
	gitRun(t, g.WorkDir(), "branch", "shared", "origin/shared")
	gitRun(t, g.WorkDir(), "checkout", "-q", "main")

	// --track would refuse here, the branch is simply checked out instead
	require.NoError(t, g.CheckoutRemote(t.Context(), "origin/shared", false))
	assert.Equal(t, "shared", headRef(t, g))
}

func TestGit_CheckoutRemote_unknownRemote(t *testing.T) {
	g := setupClone(t)
	err := g.CheckoutRemote(t.Context(), "nowhere/feature", false)
	require.ErrorContains(t, err, "no remote owns this branch")
	assert.Equal(t, "main", headRef(t, g), "a rejected checkout leaves HEAD alone")
}

// setupConflict leaves the repository in the middle of a conflicted merge.
func setupConflict(t *testing.T) *Git {
	t.Helper()
	g := setupRepo(t)
	writeRepoFile(t, g.WorkDir(), "both.txt", "alpha\nbeta\ngamma\n")
	gitRun(t, g.WorkDir(), "add", "both.txt")
	gitRun(t, g.WorkDir(), "commit", "-q", "-m", "base")
	gitRun(t, g.WorkDir(), "checkout", "-q", "-b", "feature")
	writeRepoFile(t, g.WorkDir(), "both.txt", "alpha\ntheirs\ngamma\n")
	gitRun(t, g.WorkDir(), "commit", "-q", "-am", "feature")
	gitRun(t, g.WorkDir(), "checkout", "-q", "main")
	writeRepoFile(t, g.WorkDir(), "both.txt", "alpha\nours\ngamma\n")
	gitRun(t, g.WorkDir(), "commit", "-q", "-am", "main")
	merge := exec.Command("git", "merge", "feature")
	merge.Dir = g.WorkDir()
	require.Error(t, merge.Run(), "the merge is expected to conflict")
	return g
}

func TestGit_InProgress_merge(t *testing.T) {
	g := setupConflict(t)

	st, err := g.Status(t.Context())
	require.NoError(t, err)
	assert.True(t, st.InProgress.Active())
	assert.Equal(t, StateMerging, st.InProgress.State)
	assert.Equal(t, "merging", st.InProgress.Label())
	assert.True(t, st.InProgress.CanContinue())
	require.Len(t, st.Conflicted(), 1)
	assert.Equal(t, "both.txt", st.Conflicted()[0].Path)

	require.NoError(t, g.AbortOperation(t.Context(), StateMerging))
	st, err = g.Status(t.Context())
	require.NoError(t, err)
	assert.False(t, st.InProgress.Active(), "aborting clears the state")
	assert.Empty(t, st.Conflicted())
}

func TestGit_ResolveConflict(t *testing.T) {
	for _, tt := range []struct {
		name      string
		ours      bool
		want      string
		wantStage bool // taking our side reproduces HEAD, so nothing is staged against it
	}{
		{"ours", true, "alpha\nours\ngamma\n", false},
		{"theirs", false, "alpha\ntheirs\ngamma\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := setupConflict(t)
			require.NoError(t, g.ResolveConflict(t.Context(), "both.txt", tt.ours))

			body, err := os.ReadFile(filepath.Join(g.WorkDir(), "both.txt"))
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(body), "the chosen side replaces the markers")

			st, err := g.Status(t.Context())
			require.NoError(t, err)
			assert.Empty(t, st.Conflicted(), "the path is marked resolved")
			assert.Equal(t, tt.wantStage, len(st.Staged()) == 1)
			assert.True(t, st.InProgress.Active(), "the merge still waits to be committed")
		})
	}
}

func TestInProgress_labels(t *testing.T) {
	assert.Empty(t, InProgress{}.Label())
	assert.False(t, InProgress{}.Active())
	assert.False(t, InProgress{}.CanContinue())
	assert.Equal(t, "rebasing 2/5", InProgress{State: StateRebasing, Step: 2, Total: 5}.Label())
	assert.Equal(t, "cherry-picking", InProgress{State: StateCherryPick}.Label())
	assert.False(t, InProgress{State: StateBisecting}.CanContinue(), "a bisect is reset, not continued")
	assert.True(t, InProgress{State: StateBisecting}.Active())
}

func TestAbortArgs(t *testing.T) {
	assert.Equal(t, []string{"merge", "--abort"}, abortArgs(StateMerging))
	assert.Equal(t, []string{"rebase", "--abort"}, abortArgs(StateRebasing))
	assert.Equal(t, []string{"cherry-pick", "--abort"}, abortArgs(StateCherryPick))
	assert.Equal(t, []string{"revert", "--abort"}, abortArgs(StateReverting))
	assert.Equal(t, []string{"bisect", "reset"}, abortArgs(StateBisecting), "a bisect has no abort")
	assert.Nil(t, abortArgs(StateNone))
}

func TestGit_UnstageDuringMerge(t *testing.T) {
	g := setupConflict(t)
	// an ordinary staged change made while the merge waits
	writeRepoFile(t, g.WorkDir(), "a.txt", "one\ntwo\nthree\nfour\n")
	gitRun(t, g.WorkDir(), "add", "a.txt")

	// resolving stages the conflict away
	require.NoError(t, g.ResolveConflict(t.Context(), "both.txt", false))
	st, err := g.Status(t.Context())
	require.NoError(t, err)
	require.Empty(t, st.Conflicted())

	// undoing that brings the conflict back rather than freezing the resolution
	require.NoError(t, g.UnstageDuringMerge(t.Context(), "both.txt", false))
	st, err = g.Status(t.Context())
	require.NoError(t, err)
	require.Len(t, st.Conflicted(), 1)
	assert.Equal(t, "both.txt", st.Conflicted()[0].Path)
	assert.True(t, g.hasUnmergedStages(t.Context(), "both.txt"), "the higher stages are back")
	body, err := os.ReadFile(filepath.Join(g.WorkDir(), "both.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "<<<<<<<", "the markers are back too")

	// a path that never conflicted is unstaged the ordinary way
	require.NoError(t, g.UnstageDuringMerge(t.Context(), "a.txt", false))
	st, err = g.Status(t.Context())
	require.NoError(t, err)
	assert.Empty(t, st.Staged(), "the plain staged change is gone from the index")
	assert.True(t, st.InProgress.Active(), "the merge is untouched throughout")
	assert.False(t, g.hasUnmergedStages(t.Context(), "a.txt"))
}

func TestGit_RestoreConflict_leavesCleanFilesAlone(t *testing.T) {
	g := setupConflict(t)
	writeRepoFile(t, g.WorkDir(), "a.txt", "one\ntwo\nthree\nfour\n")
	gitRun(t, g.WorkDir(), "add", "a.txt")

	require.NoError(t, g.RestoreConflict(t.Context(), "a.txt"))
	st, err := g.Status(t.Context())
	require.NoError(t, err)
	require.Len(t, st.Staged(), 1, "a file that never conflicted keeps its staging")
	assert.Equal(t, "a.txt", st.Staged()[0].Path)
}

func TestGit_ConflictRegionsAndResolve(t *testing.T) {
	g := setupConflict(t)

	regions, err := g.ConflictRegions("both.txt")
	require.NoError(t, err)
	require.Len(t, regions, 1, "the fixture leaves one block")
	assert.Equal(t, []string{"ours"}, regions[0].Ours)
	assert.Equal(t, []string{"theirs"}, regions[0].Theirs)

	require.NoError(t, g.ResolveConflictRegion("both.txt", 0, conflict.Both))
	body, err := os.ReadFile(filepath.Join(g.WorkDir(), "both.txt"))
	require.NoError(t, err)
	assert.Equal(t, "alpha\nours\ntheirs\ngamma\n", string(body), "both sides kept, markers gone")

	// with the markers gone there is nothing left to pick
	regions, err = g.ConflictRegions("both.txt")
	require.NoError(t, err)
	assert.Empty(t, regions)
	require.ErrorContains(t, g.ResolveConflictRegion("both.txt", 0, conflict.Ours), "is not in the file")

	// the path is still unmerged until it is staged
	st, err := g.Status(t.Context())
	require.NoError(t, err)
	require.Len(t, st.Conflicted(), 1)
}

func TestGit_ResolveConflictRegion_keepsTheFileMode(t *testing.T) {
	g := setupConflict(t)
	full := filepath.Join(g.WorkDir(), "both.txt")
	require.NoError(t, os.Chmod(full, 0o755)) //nolint:gosec // an executable file is the point of the test

	require.NoError(t, g.ResolveConflictRegion("both.txt", 0, conflict.Ours))
	info, err := os.Stat(full)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "an executable file stays executable")
}

// readFile reads a worktree file, failing the test when it is missing.
func readFile(t *testing.T, g *Git, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(g.WorkDir(), name)) //nolint:gosec // test helper reading a file it just wrote
	require.NoError(t, err)
	return string(b)
}

func TestGit_Rebase_e2e(t *testing.T) {
	g := setupRepo(t)
	gitRun(t, g.WorkDir(), "branch", "feature")

	// main moves on
	writeRepoFile(t, g.WorkDir(), "main.txt", "from main\n")
	gitRun(t, g.WorkDir(), "add", "main.txt")
	gitRun(t, g.WorkDir(), "commit", "-qm", "main work")

	// feature grows its own commit off the older base
	gitRun(t, g.WorkDir(), "checkout", "-q", "feature")
	writeRepoFile(t, g.WorkDir(), "feature.txt", "from feature\n")
	gitRun(t, g.WorkDir(), "add", "feature.txt")
	gitRun(t, g.WorkDir(), "commit", "-qm", "feature work")

	require.NoError(t, g.Rebase(t.Context(), "main", RebaseOpts{}))

	assert.Equal(t, "feature", headRef(t, g))
	assert.Equal(t, "from main\n", readFile(t, g, "main.txt"), "the rebase replayed feature on top of main")
	assert.Equal(t, "from feature\n", readFile(t, g, "feature.txt"))
	assert.False(t, g.InProgress(t.Context()).Active(), "a clean rebase leaves nothing unfinished")

	log, err := g.Log(t.Context(), "", 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(log), 3)
	assert.Equal(t, "feature work", log[0].Subject)
	assert.Equal(t, "main work", log[1].Subject, "feature now sits on top of main")
}

func TestGit_Rebase_conflictLeavesItInProgress(t *testing.T) {
	g := setupRepo(t)
	gitRun(t, g.WorkDir(), "branch", "feature")

	writeRepoFile(t, g.WorkDir(), "a.txt", "one\nfrom main\nthree\n")
	gitRun(t, g.WorkDir(), "commit", "-aqm", "main edit")

	gitRun(t, g.WorkDir(), "checkout", "-q", "feature")
	writeRepoFile(t, g.WorkDir(), "a.txt", "one\nfrom feature\nthree\n")
	gitRun(t, g.WorkDir(), "commit", "-aqm", "feature edit")

	err := g.Rebase(t.Context(), "main", RebaseOpts{})
	require.Error(t, err, "the same line changed on both sides")

	op := g.InProgress(t.Context())
	assert.Equal(t, StateRebasing, op.State, "the conflict UI takes it from here")

	require.NoError(t, g.AbortOperation(t.Context(), op.State))
	assert.False(t, g.InProgress(t.Context()).Active())
}

func TestGit_Rebase_autostash(t *testing.T) {
	g := setupRepo(t)
	gitRun(t, g.WorkDir(), "branch", "feature")
	writeRepoFile(t, g.WorkDir(), "main.txt", "from main\n")
	gitRun(t, g.WorkDir(), "add", "main.txt")
	gitRun(t, g.WorkDir(), "commit", "-qm", "main work")
	gitRun(t, g.WorkDir(), "checkout", "-q", "feature")

	writeRepoFile(t, g.WorkDir(), "a.txt", "one\ndirty\nthree\n")
	require.Error(t, g.Rebase(t.Context(), "main", RebaseOpts{}), "git refuses to rebase a dirty worktree")

	require.NoError(t, g.Rebase(t.Context(), "main", RebaseOpts{Autostash: true}))
	assert.Equal(t, "one\ndirty\nthree\n", readFile(t, g, "a.txt"), "the dirty work comes back")
	assert.Equal(t, "from main\n", readFile(t, g, "main.txt"))
}

func TestGit_Reset_e2e(t *testing.T) {
	t.Run("soft keeps the index and the worktree", func(t *testing.T) {
		g := setupRepo(t)
		writeRepoFile(t, g.WorkDir(), "a.txt", "one\ntwo\nchanged\n")
		gitRun(t, g.WorkDir(), "commit", "-aqm", "second")

		require.NoError(t, g.Reset(t.Context(), "HEAD~1", ResetSoft))
		assert.Equal(t, "one\ntwo\nchanged\n", readFile(t, g, "a.txt"))
		staged, err := g.HasStagedChanges(t.Context())
		require.NoError(t, err)
		assert.True(t, staged, "the undone commit is back in the index")
	})

	t.Run("mixed is the default and clears the index", func(t *testing.T) {
		g := setupRepo(t)
		writeRepoFile(t, g.WorkDir(), "a.txt", "one\ntwo\nchanged\n")
		gitRun(t, g.WorkDir(), "commit", "-aqm", "second")

		require.NoError(t, g.Reset(t.Context(), "HEAD~1", ""))
		assert.Equal(t, "one\ntwo\nchanged\n", readFile(t, g, "a.txt"))
		staged, err := g.HasStagedChanges(t.Context())
		require.NoError(t, err)
		assert.False(t, staged, "the change is back in the worktree only")
	})

	t.Run("hard discards the worktree too", func(t *testing.T) {
		g := setupRepo(t)
		writeRepoFile(t, g.WorkDir(), "a.txt", "one\ntwo\nchanged\n")
		gitRun(t, g.WorkDir(), "commit", "-aqm", "second")

		require.NoError(t, g.Reset(t.Context(), "HEAD~1", ResetHard))
		assert.Equal(t, "one\ntwo\nthree\n", readFile(t, g, "a.txt"), "back to the first commit's content")
	})

	t.Run("an unknown ref fails without moving anything", func(t *testing.T) {
		g := setupRepo(t)
		before := headRef(t, g)
		require.Error(t, g.Reset(t.Context(), "no/such/ref", ResetHard))
		assert.Equal(t, before, headRef(t, g))
	})
}
