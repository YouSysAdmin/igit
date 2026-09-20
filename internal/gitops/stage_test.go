package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/patch"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test helper with fixed arguments
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err, "git %v", args)
	return string(out)
}

func statusMap(t *testing.T, g *Git) map[string]StatusEntry {
	t.Helper()
	st, err := g.Status(t.Context())
	require.NoError(t, err)
	m := map[string]StatusEntry{}
	for _, f := range st.Files {
		m[f.Path] = f
	}
	return m
}

func TestStageUnstageFiles_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()
	writeRepoFile(t, dir, "a.txt", "one\nTWO\nthree\n")
	writeRepoFile(t, dir, "new.txt", "new\n")

	require.NoError(t, g.StageFiles(ctx, nil), "empty list is a no-op")
	require.NoError(t, g.StageFiles(ctx, []string{"a.txt", "new.txt"}))
	st := statusMap(t, g)
	assert.Equal(t, "M ", st["a.txt"].ShortStatus())
	assert.Equal(t, "A ", st["new.txt"].ShortStatus())

	require.NoError(t, g.UnstageFiles(ctx, []string{"a.txt", "new.txt"}, false))
	st = statusMap(t, g)
	assert.Equal(t, " M", st["a.txt"].ShortStatus())
	assert.True(t, st["new.txt"].Untracked)

	require.NoError(t, g.StageAll(ctx))
	assert.Len(t, statusMap(t, g), 2)
	require.NoError(t, g.UnstageAll(ctx, false))
	st = statusMap(t, g)
	assert.Equal(t, " M", st["a.txt"].ShortStatus())
	assert.True(t, st["new.txt"].Untracked)

	err := g.StageFiles(ctx, []string{"does-not-exist"})
	require.Error(t, err)
	var gerr *git.Error
	require.ErrorAs(t, err, &gerr)
}

func TestUnstage_unbornBranch_e2e(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitRun(t, dir, "init", "-q", "-b", "main")
	g := New(dir)
	ctx := t.Context()
	writeRepoFile(t, dir, "a.txt", "a\n")
	writeRepoFile(t, dir, "b.txt", "b\n")
	require.NoError(t, g.StageFiles(ctx, []string{"a.txt", "b.txt"}))
	require.NoError(t, g.UnstageFiles(ctx, []string{"a.txt"}, true))
	st := statusMap(t, g)
	assert.True(t, st["a.txt"].Untracked)
	assert.Equal(t, "A ", st["b.txt"].ShortStatus())
	require.NoError(t, g.UnstageAll(ctx, true))
	st = statusMap(t, g)
	assert.True(t, st["b.txt"].Untracked)
	assert.FileExists(t, filepath.Join(dir, "b.txt"), "unstaging keeps the worktree file")

	// DiscardAll of a staged file on an unborn branch removes it from index and disk
	require.NoError(t, g.StageFiles(ctx, []string{"b.txt"}))
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["b.txt"], DiscardAll, true))
	assert.NoFileExists(t, filepath.Join(dir, "b.txt"))
}

func TestStagingDiff_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()
	writeRepoFile(t, dir, "a.txt", "one\nTWO\nthree\n")
	writeRepoFile(t, dir, "u.txt", "u1\nu2\n")

	raw, err := g.StagingDiff(ctx, DiffSpec{Path: "a.txt"})
	require.NoError(t, err)
	assert.Contains(t, raw, "--- a/a.txt\n+++ b/a.txt\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n")

	cached, err := g.StagingDiff(ctx, DiffSpec{Path: "a.txt", Cached: true})
	require.NoError(t, err)
	assert.Empty(t, cached, "nothing staged yet")

	untracked, err := g.StagingDiff(ctx, DiffSpec{Path: "u.txt", Untracked: true, Context: 1})
	require.NoError(t, err)
	assert.Contains(t, untracked, "--- /dev/null\n+++ b/u.txt\n@@ -0,0 +1,2 @@\n+u1\n+u2\n")

	require.NoError(t, g.StageFiles(ctx, []string{"a.txt"}))
	gitRun(t, dir, "mv", "a.txt", "moved.txt")
	renamed, err := g.StagingDiff(ctx, DiffSpec{Path: "moved.txt", OrigPath: "a.txt", Cached: true})
	require.NoError(t, err)
	assert.Contains(t, renamed, "rename from a.txt\nrename to moved.txt\n")
	assert.Contains(t, renamed, "+TWO\n")

	_, err = g.StagingDiff(ctx, DiffSpec{Path: "nope.txt", Untracked: true})
	require.Error(t, err, "missing file for --no-index is a real error")
}

// changeIndices returns the patch line indices of additions/deletions whose
// stripped content is in wanted.
func changeIndices(t *testing.T, raw string, wanted ...string) []int {
	t.Helper()
	p, err := patch.Parse(raw)
	require.NoError(t, err)
	var idxs []int
	for _, v := range p.ViewLines() {
		if (v.Kind == patch.KindAddition || v.Kind == patch.KindDeletion) && slices.Contains(wanted, v.Content) {
			idxs = append(idxs, v.PatchIdx)
		}
	}
	require.NotEmpty(t, idxs)
	return idxs
}

func TestApplyLines_roundTrip_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()
	// three separate change blocks in one file
	base := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\n"
	writeRepoFile(t, dir, "a.txt", base)
	gitRun(t, dir, "add", "a.txt")
	gitRun(t, dir, "commit", "-q", "-m", "base")
	edited := "L1\nl2\nl3\nl4\nl5\nl6\nMIDDLE\nl8\nl9\nl10\nl11\nl12\nL13\n"
	writeRepoFile(t, dir, "a.txt", edited)

	raw, err := g.StagingDiff(ctx, DiffSpec{Path: "a.txt"})
	require.NoError(t, err)

	// stage only the middle block: index diff must be exactly that change
	require.NoError(t, g.ApplyLines(ctx, LineRequest{Raw: raw, Path: "a.txt", Indices: changeIndices(t, raw, "l7", "MIDDLE"), Op: StageLines}))
	cached := gitOut(t, dir, "diff", "--cached", "--no-color", "--no-ext-diff", "-U3", "--", "a.txt")
	assert.Equal(t, "diff --git a/a.txt b/a.txt\nindex "+indexLine(cached)+"\n--- a/a.txt\n+++ b/a.txt\n@@ -4,7 +4,7 @@ l3\n l4\n l5\n l6\n-l7\n+MIDDLE\n l8\n l9\n l10\n", cached)
	unstaged := gitOut(t, dir, "diff", "--no-color", "--no-ext-diff", "-U3", "--", "a.txt")
	assert.Contains(t, unstaged, "-l1\n+L1\n")
	assert.Contains(t, unstaged, "+L13\n")
	assert.NotContains(t, unstaged, "MIDDLE")

	// unstage the block again from the staged diff
	cachedRaw, err := g.StagingDiff(ctx, DiffSpec{Path: "a.txt", Cached: true})
	require.NoError(t, err)
	require.NoError(t, g.ApplyLines(ctx, LineRequest{Raw: cachedRaw, Path: "a.txt", Indices: changeIndices(t, cachedRaw, "l7", "MIDDLE"), Op: UnstageLines}))
	assert.Empty(t, gitOut(t, dir, "diff", "--cached", "--", "a.txt"))
	data, err := os.ReadFile(filepath.Join(dir, "a.txt")) //nolint:gosec // test reads a file under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, edited, string(data), "unstaging never touches the worktree")

	// discard the last block from the worktree
	raw, err = g.StagingDiff(ctx, DiffSpec{Path: "a.txt"})
	require.NoError(t, err)
	require.NoError(t, g.ApplyLines(ctx, LineRequest{Raw: raw, Path: "a.txt", Indices: changeIndices(t, raw, "L13"), Op: DiscardLines}))
	data, err = os.ReadFile(filepath.Join(dir, "a.txt")) //nolint:gosec // test reads a file under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, "L1\nl2\nl3\nl4\nl5\nl6\nMIDDLE\nl8\nl9\nl10\nl11\nl12\n", string(data))

	// context-only selection has nothing to apply
	p, err := patch.Parse(raw)
	require.NoError(t, err)
	var contextIdx int
	for _, v := range p.ViewLines() {
		if v.Kind == patch.KindContext {
			contextIdx = v.PatchIdx
			break
		}
	}
	require.Positive(t, contextIdx)
	err = g.ApplyLines(ctx, LineRequest{Raw: raw, Path: "a.txt", Indices: []int{contextIdx}, Op: StageLines})
	require.ErrorIs(t, err, ErrNoChangesSelected)
	require.Error(t, g.ApplyLines(ctx, LineRequest{Raw: "@@ broken", Path: "a.txt", Op: StageLines}))
}

// indexLine extracts the "index" header line's payload so the exact-match
// assertion does not depend on blob hashes.
func indexLine(diff string) string {
	for line := range strings.SplitSeq(diff, "\n") {
		if rest, ok := strings.CutPrefix(line, "index "); ok {
			return rest
		}
	}
	return ""
}

func TestApplyLines_untrackedPartial_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()
	writeRepoFile(t, dir, "new.txt", "alpha\nbeta\ngamma\n")
	raw, err := g.StagingDiff(ctx, DiffSpec{Path: "new.txt", Untracked: true})
	require.NoError(t, err)
	require.NoError(t, g.ApplyLines(ctx, LineRequest{Raw: raw, Path: "new.txt", Indices: changeIndices(t, raw, "alpha", "gamma"), Op: StageLines}))
	st := statusMap(t, g)
	assert.Equal(t, "AM", st["new.txt"].ShortStatus(), "partially staged new file")
	assert.Equal(t, "alpha\ngamma\n", gitOut(t, dir, "show", ":new.txt"))
}

func TestApplyLines_noNewlineAndCRLF_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()
	writeRepoFile(t, dir, "eol.txt", "x\ny")
	writeRepoFile(t, dir, "crlf.txt", "a\r\nb\r\nc\r\n")
	gitRun(t, dir, "add", "eol.txt", "crlf.txt")
	gitRun(t, dir, "commit", "-q", "-m", "eol")

	// add the missing trailing newline and stage it
	writeRepoFile(t, dir, "eol.txt", "x\ny\n")
	raw, err := g.StagingDiff(ctx, DiffSpec{Path: "eol.txt"})
	require.NoError(t, err)
	assert.Contains(t, raw, "\\ No newline at end of file")
	require.NoError(t, g.ApplyLines(ctx, LineRequest{Raw: raw, Path: "eol.txt", Indices: changeIndices(t, raw, "y"), Op: StageLines}))
	assert.Equal(t, "x\ny\n", gitOut(t, dir, "show", ":eol.txt"))

	// CRLF content and trailing whitespace survive a partial stage
	writeRepoFile(t, dir, "crlf.txt", "a\r\nB  \r\nc\r\nD\r\n")
	raw, err = g.StagingDiff(ctx, DiffSpec{Path: "crlf.txt"})
	require.NoError(t, err)
	require.NoError(t, g.ApplyLines(ctx, LineRequest{Raw: raw, Path: "crlf.txt", Indices: changeIndices(t, raw, "b\r", "B  \r"), Op: StageLines}))
	assert.Equal(t, "a\r\nB  \r\nc\r\n", gitOut(t, dir, "show", ":crlf.txt"))
}

func TestHunkIndices(t *testing.T) {
	raw := "--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@\n a\n-b\n+B\n@@ -10,2 +10,2 @@\n x\n-y\n+Y\n"
	idxs, ok := HunkIndices(raw, 4)
	require.True(t, ok)
	assert.Equal(t, []int{2, 3, 4, 5}, idxs)
	idxs, ok = HunkIndices(raw, 8)
	require.True(t, ok)
	assert.Equal(t, []int{6, 7, 8, 9}, idxs)
	_, ok = HunkIndices(raw, 0)
	assert.False(t, ok, "header line is in no hunk")
	_, ok = HunkIndices("@@ broken", 0)
	assert.False(t, ok)
}

func TestDiscard_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()

	// untracked: deleted from disk
	writeRepoFile(t, dir, "u.txt", "u\n")
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["u.txt"], DiscardUnstaged, false))
	assert.NoFileExists(t, filepath.Join(dir, "u.txt"))

	// tracked, DiscardUnstaged keeps the staged part
	writeRepoFile(t, dir, "a.txt", "one\nTWO\nthree\n")
	require.NoError(t, g.StageFiles(ctx, []string{"a.txt"}))
	writeRepoFile(t, dir, "a.txt", "one\nTWO\nTHREE\n")
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["a.txt"], DiscardUnstaged, false))
	data, err := os.ReadFile(filepath.Join(dir, "a.txt")) //nolint:gosec // test reads a file under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, "one\nTWO\nthree\n", string(data))
	assert.Equal(t, "M ", statusMap(t, g)["a.txt"].ShortStatus())

	// tracked, DiscardAll goes back to HEAD
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["a.txt"], DiscardAll, false))
	data, err = os.ReadFile(filepath.Join(dir, "a.txt")) //nolint:gosec // test reads a file under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\nthree\n", string(data))
	assert.Empty(t, statusMap(t, g))

	// new in index: DiscardUnstaged keeps the index entry, DiscardAll removes the file
	writeRepoFile(t, dir, "n.txt", "n\n")
	require.NoError(t, g.StageFiles(ctx, []string{"n.txt"}))
	writeRepoFile(t, dir, "n.txt", "n\nmore\n")
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["n.txt"], DiscardUnstaged, false))
	assert.Equal(t, "A ", statusMap(t, g)["n.txt"].ShortStatus())
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["n.txt"], DiscardAll, false))
	assert.NoFileExists(t, filepath.Join(dir, "n.txt"))
	assert.Empty(t, statusMap(t, g))

	// staged rename: DiscardAll restores the original path
	gitRun(t, dir, "mv", "a.txt", "renamed.txt")
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["renamed.txt"], DiscardAll, false))
	assert.NoFileExists(t, filepath.Join(dir, "renamed.txt"))
	assert.FileExists(t, filepath.Join(dir, "a.txt"))
	assert.Empty(t, statusMap(t, g))

	// deleted in worktree: DiscardUnstaged restores it
	require.NoError(t, os.Remove(filepath.Join(dir, "a.txt")))
	require.NoError(t, g.Discard(ctx, statusMap(t, g)["a.txt"], DiscardUnstaged, false))
	assert.FileExists(t, filepath.Join(dir, "a.txt"))
}

func TestRemoveFromWorktree_refusesEscape(t *testing.T) {
	g := New(t.TempDir())
	require.Error(t, g.removeFromWorktree("../outside"))
	require.Error(t, g.removeFromWorktree(".."))
	require.NoError(t, g.removeFromWorktree("missing-is-fine"))
	sub := filepath.Join(g.WorkDir(), "dir", "f")
	require.NoError(t, os.MkdirAll(filepath.Dir(sub), 0o700))
	require.NoError(t, os.WriteFile(sub, []byte("x"), 0o600))
	require.NoError(t, g.removeFromWorktree("dir"))
	assert.NoDirExists(t, filepath.Dir(sub))
}
