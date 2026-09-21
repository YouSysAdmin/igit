package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/session"
	"github.com/yousysadmin/igit/internal/tui"
)

func TestResumeReview(t *testing.T) {
	hist := session.New(t.TempDir())
	repo := session.Params{Path: "/repo/proj"}
	hist.Save(session.Params{Path: "/repo/proj", Ref: "main..feature", Annotations: "## a.go:1 (+)\nref review\n"})
	hist.Save(session.Params{Path: "/repo/proj", Annotations: "## b.go:2 (+)\nworktree\n"})
	hist.Save(session.Params{Path: "/repo/proj", PR: 7, Annotations: "## c.go:4 (+)\npr seven\n"})
	noPick := func([]tui.PickItem) (string, error) { t.Fatal("picker must not open"); return "", nil }

	t.Run("the scope's own entry is taken without asking", func(t *testing.T) {
		p := repo
		p.Ref = "main..feature"
		var warn strings.Builder
		e, found, err := resumeReview(hist, p, true, noPick, &warn)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "ref review", e.Records[0].Comment)
		assert.Contains(t, warn.String(), "continuing the saved review of ref-main..feature")
	})

	t.Run("a pull request picks up its draft even without --resume", func(t *testing.T) {
		p := repo
		p.PR = 7
		e, found, err := resumeReview(hist, p, false, noPick, nil)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "pr seven", e.Records[0].Comment)
	})

	t.Run("no draft for the pull request: silent", func(t *testing.T) {
		p := repo
		p.PR = 99
		_, found, err := resumeReview(hist, p, false, noPick, nil)
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("--resume without a scope entry offers the others", func(t *testing.T) {
		var offered []tui.PickItem
		p := repo
		p.Ref = "v1..v2"
		pick := func(items []tui.PickItem) (string, error) { offered = items; return items[len(items)-1].ID, nil }
		e, found, err := resumeReview(hist, p, true, pick, nil)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Len(t, offered, 3)
		assert.Equal(t, offered[2].ID, e.File)
	})

	t.Run("canceled picker and missing terminal", func(t *testing.T) {
		p := repo
		p.Ref = "v1..v2"
		_, _, err := resumeReview(hist, p, true, func([]tui.PickItem) (string, error) { return "", nil }, nil)
		require.ErrorContains(t, err, "no saved review chosen")
		_, _, err = resumeReview(hist, p, true, nil, nil)
		require.ErrorContains(t, err, "terminal is needed")
	})

	t.Run("nothing saved and disabled history", func(t *testing.T) {
		_, _, err := resumeReview(session.New(t.TempDir()), repo, true, nil, nil)
		require.ErrorContains(t, err, "no saved reviews")
		_, _, err = resumeReview(nil, repo, true, nil, nil)
		require.ErrorContains(t, err, "history is disabled")
		_, found, err := resumeReview(nil, repo, false, nil, nil)
		require.NoError(t, err)
		assert.False(t, found)
	})
}

func TestResumeReview_warnsOnCommitDrift(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "one")
	hist := session.New(t.TempDir())
	hist.Save(session.Params{Path: dir, GitRoot: dir, Annotations: "## a.go:1 (+)\nx\n"})
	gitCmd(t, dir, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "two")

	var warn strings.Builder
	_, found, err := resumeReview(hist, session.Params{Path: dir, GitRoot: dir}, true, nil, &warn)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Contains(t, warn.String(), "warning: it was saved at")
	assert.Contains(t, warn.String(), "the review is now at")
}

func TestResumeReview_pullRequestDriftFollowsTheRequestHead(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "one")
	hist := session.New(t.TempDir())
	saved := session.Params{Path: dir, GitRoot: dir, PR: 7, Commit: "aaaaaaa", Annotations: "## a.go:1 (+)\nx\n"}
	hist.Save(saved)
	// a local commit moves HEAD but says nothing about the pull request
	gitCmd(t, dir, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "two")

	var quiet strings.Builder
	_, found, err := resumeReview(hist, session.Params{Path: dir, GitRoot: dir, PR: 7, Commit: "aaaaaaa"}, true, nil, &quiet)
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotContains(t, quiet.String(), "warning:")

	var warn strings.Builder
	_, found, err = resumeReview(hist, session.Params{Path: dir, GitRoot: dir, PR: 7, Commit: "bbbbbbb"}, true, nil, &warn)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Contains(t, warn.String(), "it was saved at aaaaaaa and the review is now at bbbbbbb")
}

func TestParseArgs_ResumeAndHistoryMax(t *testing.T) {
	opts, err := parseArgs(append(noConfigArgs(t), "--resume"))
	require.NoError(t, err)
	assert.True(t, opts.Review.Resume)
	assert.Equal(t, 20, opts.Review.HistoryMax)
	_, err = parseArgs(append(noConfigArgs(t), "--resume", "--annotations", "x.md"))
	require.ErrorContains(t, err, "--resume and --annotations are mutually exclusive")
	_, err = parseArgs(append(noConfigArgs(t), "--history-max=-1"))
	require.ErrorContains(t, err, "--history-max must be >= 0")

	assert.Nil(t, historyService(options{}), "0 disables the history")
	svc := historyService(options{Review: reviewOptions{HistoryMax: 5}})
	require.NotNil(t, svc)
	assert.Equal(t, 5, svc.MaxEntries)
}

func TestHistoryParams_carriesThePullRequest(t *testing.T) {
	p := historyParams(histReq{opts: options{}, gitRoot: "/r", workDir: "/r/sub", pr: 12, prHead: "deadbee", annotations: "x"})
	assert.Equal(t, 12, p.PR)
	assert.Equal(t, "deadbee", p.Commit)
	assert.Equal(t, "/r/sub", p.Path)
	assert.Equal(t, "/r", p.GitRoot)
}

// gitCmd runs git in dir with the global config disabled and fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test helper, fixed program
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// TestScopeRef_keepsAnnotationsOutsideTheNarrowRange is the regression test for
// an incremental request review: the displayed range is narrower than the
// request's own diff, and saved annotations outside it must survive. Without
// scopeRef the preloader drops them and the history save then rewrites the
// entry with only the survivors.
func TestScopeRef_keepsAnnotationsOutsideTheNarrowRange(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "config", "user.email", "t@example.com")
	gitCmd(t, dir, "config", "user.name", "t")

	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
	}
	write("a.go", "one\ntwo\n")
	write("b.go", "one\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "base")
	base := revParse(t, dir)

	write("a.go", "one\nchanged\n") // the first review round looked at this
	gitCmd(t, dir, "commit", "-am", "round one")
	mid := revParse(t, dir)

	write("b.go", "fixed\n") // everything the author did since
	gitCmd(t, dir, "commit", "-am", "round two")
	head := revParse(t, dir)

	rec := []annot.Annotation{{File: "a.go", Line: 2, Type: "+", Comment: "from the first round"}}
	source := git.NewGit(dir)

	narrow := options{}
	narrow.Refs.Base, narrow.Refs.Against = mid, head
	require.Equal(t, mid+".."+head, narrow.ref())

	t.Run("the narrow range alone drops it", func(t *testing.T) {
		store := annot.NewStore()
		require.NoError(t, preloadRecords(rec, store, source, narrow.ref(), false, nil, nil, dir, io.Discard))
		assert.Equal(t, 0, store.Count(), "a.go is not in the range the author changed since the review")
	})

	t.Run("scopeRef keeps it", func(t *testing.T) {
		wide := narrow
		wide.prFullRef = base + ".." + head
		store := annot.NewStore()
		require.NoError(t, preloadRecords(rec, store, source, wide.scopeRef(), false, nil, nil, dir, io.Discard))
		assert.Equal(t, 1, store.Count(), "the request's own diff is the annotation scope, not the viewport")
	})

	t.Run("the history records the request range, not the viewport", func(t *testing.T) {
		wide := narrow
		wide.prFullRef = base + ".." + head
		p := historyParams(histReq{opts: wide, gitRoot: dir, workDir: dir, pr: 9, prHead: shortSHA(head), annotations: "x"})
		assert.Equal(t, base+".."+head, p.Ref)
	})
}

// revParse is the current HEAD of the test repository.
func revParse(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "HOME="+dir)
	out, err := cmd.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}
