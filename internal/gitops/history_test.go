package gitops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/git"
)

func TestStash_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()

	entries, err := g.Stashes(ctx)
	require.NoError(t, err)
	assert.Empty(t, entries)

	writeRepoFile(t, dir, "a.txt", "one\nSTASHED\nthree\n")
	writeRepoFile(t, dir, "u.txt", "untracked\n")
	require.NoError(t, g.StashPush(ctx, StashPushOpts{Message: "wip one", IncludeUntracked: true}))
	assert.Empty(t, statusMap(t, g), "everything stashed")

	writeRepoFile(t, dir, "a.txt", "one\nSECOND\nthree\n")
	require.NoError(t, g.StashPush(ctx, StashPushOpts{Message: "wip two", Paths: []string{"a.txt"}}))

	entries, err = g.Stashes(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, 0, entries[0].Index)
	assert.Equal(t, "stash@{0}", entries[0].Ref())
	assert.Contains(t, entries[0].Message, "wip two")
	assert.Equal(t, 1, entries[1].Index)
	assert.Contains(t, entries[1].Message, "wip one")
	assert.Len(t, entries[0].Hash, 40)
	assert.False(t, entries[0].Time.IsZero())

	files, err := g.StashFiles(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt"}, git.FileEntryPaths(files), "untracked files live in a third parent, not the stash diff")
	// the review renderer reads a stash file diff through the A..B ref
	lines, err := git.NewGit(dir).FileDiff(git.FileDiffRequest{Ref: StashDiffRef(1), Path: "a.txt", ContextLines: 3})
	require.NoError(t, err)
	var added []string
	for _, l := range lines {
		if l.ChangeType == git.ChangeAdd {
			added = append(added, l.Content)
		}
	}
	assert.Equal(t, []string{"STASHED"}, added)

	require.NoError(t, g.StashApply(ctx, 0))
	assert.Equal(t, " M", statusMap(t, g)["a.txt"].ShortStatus())
	entries, _ = g.Stashes(ctx)
	assert.Len(t, entries, 2, "apply keeps the entry")
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["a.txt"], DiscardUnstaged, false))
	require.NoError(t, g.StashDrop(ctx, 0))
	entries, _ = g.Stashes(ctx)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Message, "wip one")
	require.NoError(t, g.StashPop(ctx, 0))
	st := statusMap(t, g)
	assert.Equal(t, " M", st["a.txt"].ShortStatus())
	assert.True(t, st["u.txt"].Untracked)
	entries, _ = g.Stashes(ctx)
	assert.Empty(t, entries)

	require.Error(t, g.StashPop(ctx, 7))

	if g.StashStagedSupported(ctx) {
		require.NoError(t, g.StageFiles(ctx, []string{"a.txt"}))
		writeRepoFile(t, dir, "u.txt", "untracked edited\n")
		require.NoError(t, g.StashPush(ctx, StashPushOpts{Message: "index only", Staged: true}))
		st = statusMap(t, g)
		assert.False(t, st["a.txt"].HasStaged(), "index change stashed")
		assert.True(t, st["u.txt"].Untracked, "untracked file untouched by --staged")
		require.NoError(t, g.StashPop(ctx, 0))
		// pop restores the change to the worktree. commit an unrelated file, stage
		// a.txt again, edit the other file and stash only the worktree part
		writeRepoFile(t, dir, "b.txt", "b\n")
		gitRun(t, dir, "add", "b.txt")
		gitRun(t, dir, "commit", "-q", "-m", "b", "--", "b.txt")
		require.NoError(t, g.StageFiles(ctx, []string{"a.txt"}))
		writeRepoFile(t, dir, "b.txt", "B\n")
		require.NoError(t, g.StashPush(ctx, StashPushOpts{KeepIndex: true}))
		st = statusMap(t, g)
		assert.Equal(t, "M ", st["a.txt"].ShortStatus(), "--keep-index leaves the index")
		_, stillDirty := st["b.txt"]
		assert.False(t, stillDirty, "worktree-only change was stashed")
	}
}

func TestParseStashList(t *testing.T) {
	out := "stash@{0}\x1fabc\x1f1700000000\x1fOn main: wip\x00stash@{1}\x1fdef\x1f1600000000\x1fWIP on main: init\x00"
	entries, err := parseStashList(out)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, StashEntry{Index: 0, Hash: "abc", Time: entries[0].Time, Message: "On main: wip"}, entries[0])
	assert.Equal(t, int64(1700000000), entries[0].Time.Unix())
	assert.Equal(t, 1, entries[1].Index)
	_, err = parseStashList("garbage\x00")
	require.Error(t, err)
	_, err = parseStashList("stash@{x}\x1fabc\x1f1\x1fm\x00")
	require.Error(t, err)
	_, err = parseStashList("nobrace\x1fabc\x1f1\x1fm\x00")
	require.Error(t, err)
	entries, err = parseStashList("")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestBranches_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()

	require.NoError(t, g.CreateBranch(ctx, "feature", "", true))
	writeRepoFile(t, dir, "f.txt", "f\n")
	gitRun(t, dir, "add", "f.txt")
	gitRun(t, dir, "commit", "-q", "-m", "feature work")
	require.NoError(t, g.CreateBranch(ctx, "side", "main", false))

	branches, err := g.Branches(ctx)
	require.NoError(t, err)
	names := make([]string, 0, len(branches))
	var head string
	for _, b := range branches {
		names = append(names, b.Name)
		if b.Head {
			head = b.Name
		}
		assert.False(t, b.Remote)
		assert.NotEmpty(t, b.Hash)
		assert.False(t, b.Date.IsZero())
	}
	assert.ElementsMatch(t, []string{"main", "feature", "side"}, names)
	assert.Equal(t, "feature", head)
	assert.Equal(t, "feature", names[0], "most recent commit first")
	assert.Equal(t, "feature work", branches[0].Subject)

	// merge feature into main with fast-forward only
	require.NoError(t, g.Checkout(ctx, "main", false))
	require.NoError(t, g.Merge(ctx, "feature", MergeOpts{FFOnly: true}))
	assert.Equal(t, gitOut(t, dir, "rev-parse", "feature"), gitOut(t, dir, "rev-parse", "main"))

	require.NoError(t, g.RenameBranch(ctx, "side", "renamed"))
	require.NoError(t, g.DeleteBranch(ctx, "renamed", false))
	require.NoError(t, g.DeleteBranch(ctx, "feature", false))
	branches, _ = g.Branches(ctx)
	require.Len(t, branches, 1)
	assert.Equal(t, "main", branches[0].Name)

	// unmerged branch needs force
	require.NoError(t, g.CreateBranch(ctx, "dangling", "", true))
	writeRepoFile(t, dir, "d.txt", "d\n")
	gitRun(t, dir, "add", "d.txt")
	gitRun(t, dir, "commit", "-q", "-m", "dangling")
	require.NoError(t, g.Checkout(ctx, "main", false))
	require.Error(t, g.DeleteBranch(ctx, "dangling", false))
	require.NoError(t, g.DeleteBranch(ctx, "dangling", true))

	// checkout with local changes needs force
	writeRepoFile(t, dir, "a.txt", "dirty\n")
	require.NoError(t, g.CreateBranch(ctx, "other", "", false))
	gitRun(t, dir, "commit", "-q", "-am", "main moves")
	writeRepoFile(t, dir, "a.txt", "dirtier\n")
	require.Error(t, g.Checkout(ctx, "other", false), "conflicting local edit")
	require.NoError(t, g.Checkout(ctx, "other", true))
	assert.Empty(t, statusMap(t, g))

	remotes, err := g.Remotes(ctx)
	require.NoError(t, err)
	assert.Empty(t, remotes)
}

func TestBranches_remoteTracking_e2e(t *testing.T) {
	origin := setupRepo(t)
	clone := filepath.Join(t.TempDir(), "clone")
	gitRun(t, filepath.Dir(clone), "clone", "-q", origin.WorkDir(), clone)
	gitRun(t, clone, "config", "user.email", "t@e.com")
	gitRun(t, clone, "config", "user.name", "T")
	g := New(clone)
	ctx := t.Context()
	writeRepoFile(t, clone, "b.txt", "b\n")
	gitRun(t, clone, "add", "b.txt")
	gitRun(t, clone, "commit", "-q", "-m", "ahead")

	branches, err := g.Branches(ctx)
	require.NoError(t, err)
	var local, remote []Branch
	for _, b := range branches {
		if b.Remote {
			remote = append(remote, b)
		} else {
			local = append(local, b)
		}
	}
	require.Len(t, local, 1)
	assert.Equal(t, "origin/main", local[0].Upstream)
	assert.Equal(t, 1, local[0].Ahead)
	assert.Equal(t, 0, local[0].Behind)
	require.Len(t, remote, 1, "origin/HEAD symref is skipped")
	assert.Equal(t, "origin/main", remote[0].Name)

	remotes, err := g.Remotes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"origin"}, remotes)

	require.NoError(t, g.CreateBranch(ctx, "topic", "", true))
	require.NoError(t, g.SetUpstream(ctx, "topic", "origin", "main"))
	branches, _ = g.Branches(ctx)
	for _, b := range branches {
		if b.Name == "topic" {
			assert.Equal(t, "origin/main", b.Upstream)
		}
	}

	// push through the interactive command, then the remote has the branch
	cmd := g.PushCmd(PushOpts{Remote: "origin", SetUpstream: true})
	assert.Equal(t, []string{"git", "push", "--set-upstream", "origin", "HEAD"}, cmd.Args)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	originBranches, _ := origin.Branches(ctx)
	names := make([]string, 0, len(originBranches))
	for _, b := range originBranches {
		names = append(names, b.Name)
	}
	assert.Contains(t, names, "topic")

	fetch := g.FetchCmd(true, true)
	assert.Equal(t, []string{"git", "fetch", "--no-write-fetch-head", "--all", "--prune"}, fetch.Args)
	out, err = fetch.CombinedOutput()
	require.NoError(t, err, string(out))
	pull := g.PullCmd(PullOpts{Remote: "origin", Branch: "main", FFOnly: true})
	assert.Equal(t, []string{"git", "pull", "--no-edit", "--ff-only", "origin", "main"}, pull.Args)
	assert.Contains(t, pull.Env, "GIT_SEQUENCE_EDITOR=:")
	assert.Equal(t, []string{"git", "push", "--force-with-lease", "origin", "HEAD:dev"}, g.PushCmd(PushOpts{Remote: "origin", Branch: "dev", ForceWithLease: true}).Args)
	assert.Equal(t, []string{"git", "push"}, g.PushCmd(PushOpts{}).Args)
	assert.Equal(t, []string{"git", "pull", "--no-edit", "--rebase"}, g.PullCmd(PullOpts{Rebase: true}).Args)
}

func TestParseBranches(t *testing.T) {
	out := strings.Join([]string{
		"*\x1frefs/heads/main\x1fmain\x1forigin/main\x1f[ahead 2, behind 1]\x1fabc123\x1f1700000000\x1fsubject one\x1f",
		" \x1frefs/heads/gone\x1fgone\x1forigin/gone\x1f[gone]\x1fdef456\x1f1600000000\x1fold\x1f",
		" \x1frefs/remotes/origin/HEAD\x1forigin/HEAD\x1f\x1f\x1fabc123\x1f1700000000\x1fsubject one\x1frefs/remotes/origin/main",
		" \x1frefs/remotes/origin/main\x1forigin/main\x1f\x1f\x1fabc123\x1f1700000000\x1fsubject one\x1f",
		"short\x1fline",
	}, "\n") + "\n"
	bs := parseBranches(out)
	require.Len(t, bs, 3)
	assert.Equal(t, Branch{Name: "main", Upstream: "origin/main", Hash: "abc123", Subject: "subject one", Ahead: 2, Behind: 1, Head: true, Date: bs[0].Date}, bs[0])
	assert.True(t, bs[1].UpstreamGone)
	assert.Equal(t, "origin/main", bs[2].Name)
	assert.True(t, bs[2].Remote)
	assert.Empty(t, parseBranches(""))
}

func TestLog_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()
	writeRepoFile(t, dir, "a.txt", "one\nTWO\nthree\n")
	gitRun(t, dir, "commit", "-q", "-am", "second commit", "-m", "with a body\nsecond line")
	gitRun(t, dir, "tag", "v1")

	commits, err := g.Log(ctx, "", 0)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	assert.Equal(t, "second commit", commits[0].Subject)
	assert.Equal(t, "with a body\nsecond line", commits[0].Body)
	assert.Empty(t, commits[1].Body)
	assert.Equal(t, "Test", commits[0].Author)
	assert.Equal(t, "test@example.com", commits[0].Email)
	assert.Len(t, commits[0].Hash, 40)
	assert.Equal(t, commits[0].Hash[:7], commits[0].ShortHash[:7])
	assert.Equal(t, []string{commits[1].Hash}, commits[0].Parents)
	assert.ElementsMatch(t, []string{"main", "v1"}, commits[0].Refs)
	assert.Empty(t, commits[1].Parents)
	assert.Equal(t, commits[1].Hash+".."+commits[0].Hash, commits[0].DiffRef())
	assert.Equal(t, emptyTree+".."+commits[1].Hash, commits[1].DiffRef(), "root commit diffs against the empty tree")

	files, err := g.CommitFiles(ctx, commits[0].Hash)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt"}, git.FileEntryPaths(files))
	files, err = g.CommitFiles(ctx, commits[1].Hash)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt"}, git.FileEntryPaths(files), "--root lists the root commit's files")

	// the review renderer shows a commit file through the parent..hash ref
	lines, err := git.NewGit(dir).FileDiff(git.FileDiffRequest{Ref: commits[0].DiffRef(), Path: "a.txt", ContextLines: 3})
	require.NoError(t, err)
	var added []string
	for _, l := range lines {
		if l.ChangeType == git.ChangeAdd {
			added = append(added, l.Content)
		}
	}
	assert.Equal(t, []string{"TWO"}, added)

	limited, err := g.Log(ctx, "HEAD", 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)
	_, err = g.Log(ctx, "nope-ref", 5)
	require.Error(t, err)
}

func TestLog_unborn_e2e(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitRun(t, dir, "init", "-q", "-b", "main")
	commits, err := New(dir).Log(t.Context(), "", 10)
	require.NoError(t, err)
	assert.Empty(t, commits)
}

func TestParseLog(t *testing.T) {
	out := "h1\x1fs1\x1fAlice\x1fa@x\x1f1700000000\x1fp1 p2\x1fHEAD -> main, tag: v1, origin/main\x1fmerge it\x1fbody line\n\nmore\n\x00" +
		"h2\x1fs2\x1fBob\x1fb@x\x1f1600000000\x1f\x1f\x1froot\x1f\x00"
	commits, err := parseLog(out)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	assert.Equal(t, "body line\n\nmore", commits[0].Body)
	assert.Equal(t, "merge it\n\nbody line\n\nmore", commits[0].Message())
	assert.Empty(t, commits[1].Body)
	assert.Equal(t, "root", commits[1].Message())
	assert.Equal(t, []string{"p1", "p2"}, commits[0].Parents)
	assert.Equal(t, []string{"main", "v1", "origin/main"}, commits[0].Refs)
	assert.Equal(t, "merge it", commits[0].Subject)
	assert.Nil(t, commits[1].Parents)
	assert.Nil(t, commits[1].Refs)
	_, err = parseLog("bad\x00")
	require.Error(t, err)
}
