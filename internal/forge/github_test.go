package forge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
)

// call records one command the fake runner saw.
type call struct {
	name  string
	args  []string
	stdin string
}

// fakeRunner answers commands from a table keyed by "name arg0 arg1..." prefix
// and records every call.
type fakeRunner struct {
	calls   []call
	answers map[string]string // key prefix -> stdout
	first   map[string]string // key prefix -> stdout for the first matching call only
	fail    map[string]string // key prefix -> stderr (error)
}

func (f *fakeRunner) run(_ context.Context, _, name, stdin string, args ...string) (string, error) {
	f.calls = append(f.calls, call{name: name, args: args, stdin: stdin})
	key := name + " " + strings.Join(args, " ")
	for prefix, out := range f.first {
		if strings.HasPrefix(key, prefix) {
			delete(f.first, prefix)
			return out, nil
		}
	}
	for prefix, stderr := range f.fail {
		if strings.HasPrefix(key, prefix) {
			return "", &CommandError{Name: name, Args: args, Stderr: stderr, Err: errors.New("exit status 1")}
		}
	}
	for prefix, out := range f.answers {
		if strings.HasPrefix(key, prefix) {
			return out, nil
		}
	}
	return "", nil
}

func (f *fakeRunner) has(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c.name+" "+strings.Join(c.args, " "), prefix) {
			return true
		}
	}
	return false
}

const prJSON = `{"number":12,"url":"https://github.com/acme/widgets/pull/12","title":" Fix parser ","baseRefName":"main","headRefName":"fix-parser","headRefOid":"abc123"}`

const fileDiff = `diff --git a/pkg/a.go b/pkg/a.go
index 1..2 100644
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -10,7 +10,8 @@ func f() {
 ctx1
 ctx2
-old
+new1
+new2
 ctx3
 ctx4
 ctx5
`

func TestParsePullURL(t *testing.T) {
	owner, repo, err := parsePullURL("https://github.com/acme/widgets/pull/12")
	require.NoError(t, err)
	assert.Equal(t, "acme", owner)
	assert.Equal(t, "widgets", repo)
	_, _, err = parsePullURL("https://example.com/x")
	require.Error(t, err)
}

func TestPullRequest_RefAndLabel(t *testing.T) {
	pr := PullRequest{Number: 7, Title: "T", MergeBase: "base", HeadSHA: "head"}
	assert.Equal(t, "base..head", pr.Ref())
	assert.Equal(t, "PR #7: T", pr.Label())
	assert.Equal(t, "PR #7", PullRequest{Number: 7}.Label())
}

func TestGitHub_Resolve(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"gh pr view": prJSON}}
	g := NewWithRunner("/repo", f.run)
	pr, err := g.Resolve(t.Context(), "12")
	require.NoError(t, err)
	assert.Equal(t, PullRequest{Forge: KindGitHub, Owner: "acme", Repo: "widgets", Number: 12, URL: "https://github.com/acme/widgets/pull/12",
		Title: "Fix parser", BaseRef: "main", HeadRef: "fix-parser", HeadSHA: "abc123"}, pr)
	assert.Equal(t, []string{"pr", "view", "12", "--json", "number,url,title,baseRefName,headRefName,headRefOid"}, f.calls[0].args)

	// without a ref gh resolves the current branch
	_, err = g.Resolve(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, "view", f.calls[1].args[1])
	assert.Equal(t, "--json", f.calls[1].args[2])

	f.fail = map[string]string{"gh pr view": "no pull requests found for branch \"main\"\nmore"}
	_, err = g.Resolve(t.Context(), "")
	require.ErrorContains(t, err, "no pull requests found")
	assert.NotContains(t, err.Error(), "more", "only the first stderr line is in the message")
	var cerr *CommandError
	require.ErrorAs(t, err, &cerr)
	assert.Contains(t, cerr.Detail(), "more")
}

func TestGitHub_Prepare(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{
		"git remote -v":  "upstream\tgit@github.com:Acme/Widgets.git (fetch)\nupstream\tgit@github.com:Acme/Widgets.git (push)\norigin\thttps://github.com/me/widgets.git (fetch)\n",
		"git merge-base": "mb0000\n",
	}}
	g := NewWithRunner("/repo", f.run)
	pr := PullRequest{Owner: "acme", Repo: "widgets", Number: 12, BaseRef: "main", HeadSHA: "abc123"}
	require.NoError(t, g.Prepare(t.Context(), &pr))
	assert.Equal(t, "upstream", pr.Remote, "the remote whose url names the repository, matched case-insensitively")
	assert.Equal(t, "mb0000", pr.MergeBase)
	assert.True(t, f.has("git fetch --no-tags --no-write-fetch-head upstream +refs/pull/12/head:refs/remotes/upstream/pr/12 +refs/heads/main:refs/remotes/upstream/main"))
	assert.True(t, f.has("git merge-base upstream/main abc123"))

	// no matching remote falls back to origin. a failed fetch is reported
	f2 := &fakeRunner{answers: map[string]string{"git remote -v": "origin\tgit@github.com:other/repo.git (fetch)\n"}, fail: map[string]string{"git fetch": "fatal: couldn't find remote ref"}}
	pr2 := PullRequest{Owner: "acme", Repo: "widgets", Number: 3, BaseRef: "main", HeadSHA: "h"}
	err := NewWithRunner("/repo", f2.run).Prepare(t.Context(), &pr2)
	require.ErrorContains(t, err, "fetch pull request #3 from origin")
}

func TestGitHub_Plan(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"git diff": fileDiff}}
	g := NewWithRunner("/repo", f.run)
	pr := PullRequest{MergeBase: "mb", HeadSHA: "head"}
	annots := []annot.Annotation{
		{File: "pkg/a.go", Line: 13, Type: "+", Comment: "added line"},         // new1
		{File: "pkg/a.go", Line: 12, Type: "-", Comment: "removed line"},       // old
		{File: "pkg/a.go", Line: 11, Type: " ", Comment: "context line"},       // ctx2 (new numbering)
		{File: "pkg/a.go", Line: 13, EndLine: 14, Type: "+", Comment: "range"}, // new1..new2
		{File: "pkg/a.go", Line: 13, EndLine: 40, Type: "+", Comment: "range end outside collapses"},
		{File: "pkg/a.go", Line: 99, Type: " ", Comment: "far away"},
		{File: "pkg/a.go", Line: 0, Comment: "whole file"},
	}
	plan, err := g.Plan(t.Context(), pr, annots)
	require.NoError(t, err)
	require.Len(t, plan.Comments, 5)
	assert.Equal(t, Comment{Path: "pkg/a.go", Body: "added line", Line: 13, Side: "RIGHT"}, plan.Comments[0])
	assert.Equal(t, Comment{Path: "pkg/a.go", Body: "removed line", Line: 12, Side: "LEFT"}, plan.Comments[1])
	assert.Equal(t, Comment{Path: "pkg/a.go", Body: "context line", Line: 11, Side: "RIGHT"}, plan.Comments[2])
	assert.Equal(t, Comment{Path: "pkg/a.go", Body: "range", Line: 14, Side: "RIGHT", StartLine: 13, StartSide: "RIGHT"}, plan.Comments[3])
	assert.Equal(t, Comment{Path: "pkg/a.go", Body: "range end outside collapses", Line: 13, Side: "RIGHT"}, plan.Comments[4])
	require.Len(t, plan.Outside, 2)
	assert.Equal(t, 99, plan.Outside[0].Line)
	assert.Equal(t, 0, plan.Outside[1].Line)

	diffCalls := 0
	for _, c := range f.calls {
		if c.name == "git" && c.args[0] == "diff" {
			diffCalls++
			assert.Equal(t, []string{"diff", "--no-color", "--no-ext-diff", "-U3", "mb", "head", "--", "pkg/a.go"}, c.args)
		}
	}
	assert.Equal(t, 1, diffCalls, "one diff per annotated file")
}

func TestGitHub_Plan_emptyDiffAndErrors(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"git diff": "\n"}}
	g := NewWithRunner("/repo", f.run)
	plan, err := g.Plan(t.Context(), PullRequest{}, []annot.Annotation{{File: "gone.go", Line: 1, Type: "+", Comment: "x"}})
	require.NoError(t, err)
	assert.Empty(t, plan.Comments)
	assert.Len(t, plan.Outside, 1, "a file without a diff cannot anchor anything")

	f.fail = map[string]string{"git diff": "fatal: bad revision"}
	_, err = g.Plan(t.Context(), PullRequest{}, []annot.Annotation{{File: "a.go", Line: 1, Type: "+"}})
	require.ErrorContains(t, err, "diff a.go")
}

func TestGitHub_Submit(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{
		"git diff": fileDiff,
		"gh api":   `{"id":1,"html_url":"https://github.com/acme/widgets/pull/12#pullrequestreview-1"}`,
	}}
	g := NewWithRunner("/repo", f.run)
	pr := PullRequest{Owner: "acme", Repo: "widgets", Number: 12, URL: "https://github.com/acme/widgets/pull/12", MergeBase: "mb", HeadSHA: "abc123"}
	annots := []annot.Annotation{
		{File: "pkg/a.go", Line: 13, Type: "+", Comment: "added"},
		{File: "pkg/a.go", Line: 99, Type: " ", Comment: "far"},
		{File: "pkg/a.go", Line: 0, Comment: "file note"},
	}
	sub, err := g.Submit(t.Context(), pr, EventRequestChanges, annots)
	require.NoError(t, err)
	assert.Equal(t, Submission{URL: "https://github.com/acme/widgets/pull/12#pullrequestreview-1", Event: EventRequestChanges, Comments: 1, InBody: 2}, sub)

	var api call
	for _, c := range f.calls {
		if c.name == "gh" {
			api = c
		}
	}
	assert.Equal(t, []string{"api", "-X", "POST", "-H", "Accept: application/vnd.github+json", "repos/acme/widgets/pulls/12/reviews", "--input", "-"}, api.args)
	var review Review
	require.NoError(t, json.Unmarshal([]byte(api.stdin), &review))
	assert.Equal(t, "abc123", review.CommitID)
	assert.Equal(t, "REQUEST_CHANGES", review.Event)
	require.Len(t, review.Comments, 1)
	assert.Equal(t, Comment{Path: "pkg/a.go", Body: "added", Line: 13, Side: "RIGHT"}, review.Comments[0])
	assert.Contains(t, review.Body, "- `pkg/a.go`: file note")
	assert.Contains(t, review.Body, "- `pkg/a.go:99`: far")
	assert.Less(t, strings.Index(review.Body, "`pkg/a.go`:"), strings.Index(review.Body, "`pkg/a.go:99`"), "file notes sort first")
	assert.NotContains(t, api.stdin, "start_line", "single-line comments carry no range fields")
}

func TestGitHub_Submit_errorsAndFallbackURL(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"git diff": fileDiff, "gh api": "not json"}}
	g := NewWithRunner("/repo", f.run)
	pr := PullRequest{Owner: "a", Repo: "b", Number: 1, URL: "https://github.com/a/b/pull/1", MergeBase: "mb", HeadSHA: "h"}
	sub, err := g.Submit(t.Context(), pr, EventApprove, nil)
	require.NoError(t, err)
	assert.Equal(t, pr.URL, sub.URL, "an unparsable response falls back to the pull request url")
	assert.Equal(t, 0, sub.Comments)

	f.fail = map[string]string{"gh api": "HTTP 422: Validation Failed\n{\"message\":\"Unprocessable\"}"}
	_, err = g.Submit(t.Context(), pr, EventApprove, nil)
	require.ErrorContains(t, err, "post review")
	assert.ErrorContains(t, err, "HTTP 422")
}

func TestBodyFor(t *testing.T) {
	assert.Empty(t, bodyFor(nil))
	body := bodyFor([]annot.Annotation{
		{File: "z.go", Line: 5, EndLine: 9, Comment: "range"},
		{File: "a.go", Line: 3, Comment: " trimmed \n"},
	})
	assert.Equal(t, "Notes that do not map to a diff line:\n\n- `a.go:3`: trimmed\n- `z.go:5-9`: range", body)
}

func TestCommandError(t *testing.T) {
	e := &CommandError{Name: "gh", Args: []string{"api"}, Err: errors.New("exit status 1")}
	assert.Equal(t, "gh api: exit status 1", e.Error(), "without stderr the exec error is used")
	assert.Equal(t, "exit status 1", e.Unwrap().Error())
}

func TestGitHub_Comments(t *testing.T) {
	pages := `[[{"path":"a.go","line":13,"side":"RIGHT","body":" looks off ","user":{"login":"alice"}},
	{"path":"a.go","line":null,"original_line":3,"side":"RIGHT","body":"outdated","user":{"login":"bob"}},
	{"path":"b.go","line":20,"start_line":18,"side":"LEFT","body":"range","user":{"login":"carol"}},
	{"path":"c.go","line":4,"body":"no side","user":{"login":"dan"}}],
	[{"path":"d.go","line":1,"side":"RIGHT","body":"page two","user":{"login":"eve"}}]]`
	f := &fakeRunner{answers: map[string]string{"gh api --paginate --slurp": pages}}
	g := NewWithRunner("/repo", f.run)
	got, err := g.Comments(t.Context(), PullRequest{Owner: "o", Repo: "r", Number: 3})
	require.NoError(t, err)
	assert.Equal(t, []LineComment{
		{Path: "a.go", Line: 13, Side: "RIGHT", Author: "alice", Body: "looks off"},
		{Path: "b.go", Line: 20, StartLine: 18, Side: "LEFT", Author: "carol", Body: "range"},
		{Path: "c.go", Line: 4, Side: "RIGHT", Author: "dan", Body: "no side"},
		{Path: "d.go", Line: 1, Side: "RIGHT", Author: "eve", Body: "page two"},
	}, got, "outdated comments are dropped, every page is read, a missing side means RIGHT")
	assert.Contains(t, strings.Join(f.calls[0].args, " "), "repos/o/r/pulls/3/comments")

	empty := &fakeRunner{answers: map[string]string{"gh api": ""}}
	got, err = NewWithRunner("/repo", empty.run).Comments(t.Context(), PullRequest{})
	require.NoError(t, err)
	assert.Empty(t, got)

	broken := &fakeRunner{fail: map[string]string{"gh api": "HTTP 404"}}
	_, err = NewWithRunner("/repo", broken.run).Comments(t.Context(), PullRequest{})
	require.ErrorContains(t, err, "list review comments")
}

func TestGitHub_ListOpen(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"gh pr list": `[{"number":9,"title":" Nine ","headRefName":"nine","isDraft":true,"author":{"login":"al"}},{"number":8,"title":"Eight","headRefName":"eight","isDraft":false,"author":{"login":"bo"}}]`}}
	list, err := NewWithRunner("/repo", f.run).ListOpen(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []Summary{{Number: 9, Title: "Nine", Author: "al", Branch: "nine", Draft: true}, {Number: 8, Title: "Eight", Author: "bo", Branch: "eight"}}, list)
	assert.Equal(t, []string{"pr", "list", "--state", "open", "--limit", "100", "--json", "number,title,author,headRefName,isDraft"}, f.calls[0].args)
}

func TestIsNoPullRequest(t *testing.T) {
	assert.True(t, IsNoPullRequest(errors.New("gh pr view: no pull requests found for branch \"main\"")))
	assert.False(t, IsNoPullRequest(errors.New("HTTP 401")))
	assert.False(t, IsNoPullRequest(nil))
}
