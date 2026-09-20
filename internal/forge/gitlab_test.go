package forge

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
)

const mrJSON = `{"iid":7,"title":" Add thing ","web_url":"https://gitlab.com/grp/sub/proj/-/merge_requests/7","source_branch":"feat","target_branch":"main","sha":"headsha","diff_refs":{"base_sha":"basesha","head_sha":"headsha","start_sha":"startsha"},"references":{"full":"grp/sub/proj!7"},"draft":false}`

const mrAPI = "projects/grp%2Fsub%2Fproj/merge_requests/7"

func resolvedMR() PullRequest {
	return PullRequest{
		Forge: KindGitLab, Project: "grp/sub/proj", Number: 7, URL: "https://gitlab.com/grp/sub/proj/-/merge_requests/7", Title: "Add thing",
		BaseRef: "main", HeadRef: "feat", HeadSHA: "headsha", BaseSHA: "basesha", StartSHA: "startsha", MergeBase: "basesha", Remote: "origin",
	}
}

func TestMergeRequestArg(t *testing.T) {
	assert.Equal(t, "7", mergeRequestArg("https://gitlab.com/grp/sub/proj/-/merge_requests/7"))
	assert.Equal(t, "7", mergeRequestArg("https://gitlab.com/grp/sub/proj/-/merge_requests/7/diffs"))
	assert.Equal(t, "7", mergeRequestArg("!7"))
	assert.Equal(t, "7", mergeRequestArg("7"))
	assert.Equal(t, "feat", mergeRequestArg("feat"))
	assert.Empty(t, mergeRequestArg(""))
}

func TestProjectPath(t *testing.T) {
	assert.Equal(t, "grp/sub/proj", projectPath("grp/sub/proj!7", ""))
	assert.Equal(t, "grp/sub/proj", projectPath("", "https://gitlab.com/grp/sub/proj/-/merge_requests/7"))
	assert.Empty(t, projectPath("", "https://gitlab.com/"))
}

func TestParseGitLabVersion(t *testing.T) {
	v, ok := parseGitLabVersion("16.7.0")
	require.True(t, ok)
	assert.Equal(t, gitlabVersion{16, 7}, v)
	v, ok = parseGitLabVersion("19.5.0-pre")
	require.True(t, ok)
	assert.Equal(t, gitlabVersion{19, 5}, v)
	_, ok = parseGitLabVersion("nope")
	assert.False(t, ok)
	assert.True(t, gitlabVersion{17, 0}.atLeast(17, 0))
	assert.False(t, gitlabVersion{16, 11}.atLeast(17, 0))
}

func TestGitLab_Resolve(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"glab mr view 7 -F json": mrJSON, "glab api version": `{"version":"16.7.0"}`}}
	g := NewGitLabWithRunner("/repo", f.run)
	pr, err := g.Resolve(t.Context(), "https://gitlab.com/grp/sub/proj/-/merge_requests/7")
	require.NoError(t, err)
	assert.Equal(t, KindGitLab, pr.Forge)
	assert.Equal(t, "grp/sub/proj", pr.Project)
	assert.Equal(t, 7, pr.Number)
	assert.Equal(t, "Add thing", pr.Title, "title is trimmed")
	assert.Equal(t, "main", pr.BaseRef)
	assert.Equal(t, "feat", pr.HeadRef)
	assert.Equal(t, "headsha", pr.HeadSHA)
	assert.Equal(t, "basesha", pr.BaseSHA)
	assert.Equal(t, "startsha", pr.StartSHA)
	assert.Equal(t, "MR !7: Add thing", pr.Label())
	assert.True(t, f.has("glab mr view 7 -F json"), "the URL is reduced to its iid")
	assert.Equal(t, []Event{EventComment, EventApprove}, g.Verdicts(), "16.7 cannot request changes")

	f2 := &fakeRunner{answers: map[string]string{"glab mr view": mrJSON, "glab api version": `{"version":"19.5.0-pre"}`}}
	g2 := NewGitLabWithRunner("/repo", f2.run)
	_, err = g2.Resolve(t.Context(), "")
	require.NoError(t, err)
	assert.True(t, f2.has("glab mr view -F json"), "no ref means the current branch")
	assert.Equal(t, allVerdicts, g2.Verdicts())

	f3 := &fakeRunner{answers: map[string]string{"glab mr view": mrJSON}}
	g3 := NewGitLabWithRunner("/repo", f3.run)
	_, err = g3.Resolve(t.Context(), "!7")
	require.NoError(t, err)
	assert.True(t, f3.has("glab mr view 7 -F json"))
	assert.Equal(t, allVerdicts, g3.Verdicts(), "an unknown version is treated as current")

	f4 := &fakeRunner{fail: map[string]string{"glab mr view": `No open merge request available for "master".`}}
	_, err = NewGitLabWithRunner("/repo", f4.run).Resolve(t.Context(), "")
	require.ErrorContains(t, err, "resolve merge request")
	assert.True(t, NewGitLab("/repo").IsNoRequest(err))
	assert.False(t, NewGitLab("/repo").IsNoRequest(nil))

	f5 := &fakeRunner{answers: map[string]string{"glab mr view": `{"iid":7,"title":"x","web_url":"https://gitlab.com/g/p/-/merge_requests/7"}`}}
	_, err = NewGitLabWithRunner("/repo", f5.run).Resolve(t.Context(), "7")
	require.ErrorContains(t, err, "no head commit or target branch")
}

func TestGitLab_Prepare(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"git remote -v": "upstream\tgit@gitlab.com:grp/sub/proj.git (fetch)\norigin\tgit@gitlab.com:me/proj.git (fetch)\n"}}
	g := NewGitLabWithRunner("/repo", f.run)
	pr := resolvedMR()
	pr.MergeBase, pr.Remote = "", ""
	require.NoError(t, g.Prepare(t.Context(), &pr))
	assert.Equal(t, "upstream", pr.Remote, "the remote naming the project wins")
	assert.True(t, f.has("git fetch --no-tags --no-write-fetch-head upstream +refs/merge-requests/7/head:refs/remotes/upstream/mr/7 +refs/heads/main:refs/remotes/upstream/main"))
	assert.Equal(t, "basesha", pr.MergeBase, "the service's merge base is used when the clone has it")
	assert.False(t, f.has("git merge-base"))
	assert.False(t, f.has("git rev-parse"), "start_sha came from the service")

	f2 := &fakeRunner{answers: map[string]string{"git merge-base": "mb\n", "git rev-parse": "startlocal\n"}, fail: map[string]string{"git cat-file": "missing"}}
	pr2 := resolvedMR()
	pr2.MergeBase, pr2.StartSHA = "", ""
	require.NoError(t, NewGitLabWithRunner("/repo", f2.run).Prepare(t.Context(), &pr2))
	assert.Equal(t, "origin", pr2.Remote, "no matching remote falls back to origin")
	assert.Equal(t, "mb", pr2.MergeBase, "git computes the merge base when the service's commit is not local")
	assert.Equal(t, "mb", pr2.BaseSHA)
	assert.Equal(t, "startlocal", pr2.StartSHA)
	assert.True(t, f2.has("git rev-parse origin/main"))

	f3 := &fakeRunner{fail: map[string]string{"git fetch": "fatal: couldn't find remote ref"}}
	pr3 := resolvedMR()
	err := NewGitLabWithRunner("/repo", f3.run).Prepare(t.Context(), &pr3)
	require.ErrorContains(t, err, "fetch merge request !7")
}

// draftNotes decodes the draft notes the fake runner saw, in order.
func draftNotes(t *testing.T, f *fakeRunner) []draftNote {
	t.Helper()
	var notes []draftNote
	for _, c := range f.calls {
		if c.name == "glab" && strings.Contains(strings.Join(c.args, " "), "/draft_notes --input -") {
			var n draftNote
			require.NoError(t, json.Unmarshal([]byte(c.stdin), &n))
			notes = append(notes, n)
		}
	}
	return notes
}

func TestGitLab_Submit_comment(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"git diff": fileDiff}}
	g := NewGitLabWithRunner("/repo", f.run)
	annots := []annot.Annotation{
		{File: "pkg/a.go", Line: 12, Type: "+", Comment: "added"},
		{File: "pkg/a.go", Line: 12, Type: "-", Comment: "removed"},
		{File: "pkg/a.go", Line: 14, Type: " ", Comment: "context"},
		{File: "pkg/a.go", Line: 12, EndLine: 13, Type: "+", Comment: "range"},
		{File: "pkg/a.go", Line: 999, Type: "+", Comment: "outside"},
		{File: "pkg/a.go", Line: 0, Comment: "whole file"},
	}
	sub, err := g.Submit(t.Context(), resolvedMR(), EventComment, annots)
	require.NoError(t, err)
	assert.Equal(t, Submission{URL: resolvedMR().URL, Event: EventComment, Comments: 4, InBody: 2}, sub)

	notes := draftNotes(t, f)
	require.Len(t, notes, 5, "four anchored notes and one review note")
	added, removed, ctx, rng, body := notes[0], notes[1], notes[2], notes[3], notes[4]
	assert.Equal(t, &position{BaseSHA: "basesha", HeadSHA: "headsha", StartSHA: "startsha", PositionType: "text", NewPath: "pkg/a.go", OldPath: "pkg/a.go", NewLine: 12}, added.Position)
	assert.Equal(t, &position{BaseSHA: "basesha", HeadSHA: "headsha", StartSHA: "startsha", PositionType: "text", NewPath: "pkg/a.go", OldPath: "pkg/a.go", OldLine: 12}, removed.Position)
	assert.Equal(t, 14, ctx.Position.NewLine)
	assert.Equal(t, 13, ctx.Position.OldLine, "an unchanged line carries both numbers")
	assert.Equal(t, 12, rng.Position.NewLine, "a range anchors on its first line")
	assert.Equal(t, "lines 12-13: range", rng.Note)
	assert.Nil(t, body.Position, "the review note has no anchor")
	assert.Contains(t, body.Note, "`pkg/a.go:999`: outside")
	assert.Contains(t, body.Note, "`pkg/a.go`: whole file")

	assert.True(t, f.has("glab api -X POST "+mrAPI+"/draft_notes/bulk_publish"), "one publication for every note")
	assert.False(t, f.has("glab api -X POST "+mrAPI+"/approve"))
	assert.False(t, f.has("glab api graphql"))
}

func TestGitLab_Submit_verdicts(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"git diff": fileDiff, "glab api graphql": `{"data":{"mergeRequestRequestChanges":{"errors":[]}}}`}}
	g := NewGitLabWithRunner("/repo", f.run)
	annots := []annot.Annotation{{File: "pkg/a.go", Line: 12, Type: "+", Comment: "added"}}

	_, err := g.Submit(t.Context(), resolvedMR(), EventApprove, annots)
	require.NoError(t, err)
	var approve *call
	for i := range f.calls {
		if strings.Contains(strings.Join(f.calls[i].args, " "), mrAPI+"/approve --input -") {
			approve = &f.calls[i]
		}
	}
	require.NotNil(t, approve, "approve is posted after publishing")
	assert.JSONEq(t, `{"sha":"headsha"}`, approve.stdin, "approval pins the reviewed head")

	f.calls = nil
	_, err = g.Submit(t.Context(), resolvedMR(), EventRequestChanges, annots)
	require.NoError(t, err)
	var graphql *call
	for i := range f.calls {
		if f.calls[i].name == "glab" && len(f.calls[i].args) > 1 && f.calls[i].args[1] == "graphql" {
			graphql = &f.calls[i]
		}
	}
	require.NotNil(t, graphql)
	query := strings.Join(graphql.args, " ")
	assert.Contains(t, query, `mergeRequestRequestChanges(input: {projectPath: "grp/sub/proj", iid: "7"})`)

	// nothing to publish, a bare verdict still lands
	f.calls = nil
	_, err = g.Submit(t.Context(), resolvedMR(), EventApprove, nil)
	require.NoError(t, err)
	assert.False(t, f.has("glab api -X POST "+mrAPI+"/draft_notes"), "no notes, no publication")
	assert.True(t, f.has("glab api -X POST "+mrAPI+"/approve"))
}

func TestGitLab_Submit_errors(t *testing.T) {
	annots := []annot.Annotation{{File: "pkg/a.go", Line: 12, Type: "+", Comment: "added"}}

	f := &fakeRunner{answers: map[string]string{"git diff": fileDiff, "glab api graphql": `{"errors":[{"message":"field mergeRequestRequestChanges doesn't exist"}]}`}}
	_, err := NewGitLabWithRunner("/repo", f.run).Submit(t.Context(), resolvedMR(), EventRequestChanges, annots)
	require.ErrorContains(t, err, "request changes")
	require.ErrorContains(t, err, "notes are published")

	f2 := &fakeRunner{answers: map[string]string{"git diff": fileDiff, "glab api graphql": `{"data":{"mergeRequestRequestChanges":{"errors":["not a reviewer"]}}}`}}
	_, err = NewGitLabWithRunner("/repo", f2.run).Submit(t.Context(), resolvedMR(), EventRequestChanges, annots)
	require.ErrorContains(t, err, "not a reviewer")

	// a user who is not a reviewer yet is added and the verdict retried
	f5 := &fakeRunner{
		first:   map[string]string{"glab api graphql -f query=mutation { mergeRequestRequestChanges": `{"data":{"mergeRequestRequestChanges":{"errors":["Reviewer not found"]}}}`},
		answers: map[string]string{"git diff": fileDiff, "glab api user": `{"id":7,"username":"me"}`, "glab api graphql": `{"data":{"mergeRequestRequestChanges":{"errors":[]},"mergeRequestSetReviewers":{"errors":[]}}}`},
	}
	sub, err := NewGitLabWithRunner("/repo", f5.run).Submit(t.Context(), resolvedMR(), EventRequestChanges, annots)
	require.NoError(t, err)
	assert.Equal(t, EventRequestChanges, sub.Event)
	var mutations []string
	for _, c := range f5.calls {
		if c.name == "glab" && len(c.args) > 1 && c.args[1] == "graphql" {
			mutations = append(mutations, strings.Join(c.args, " "))
		}
	}
	require.Len(t, mutations, 3, "request, add reviewer, request again")
	assert.Contains(t, mutations[1], `mergeRequestSetReviewers(input: {projectPath: "grp/sub/proj", iid: "7", reviewerUsernames: ["me"], operationMode: APPEND})`)
	assert.Contains(t, mutations[2], "mergeRequestRequestChanges")

	f6 := &fakeRunner{
		first:   map[string]string{"glab api graphql -f query=mutation { mergeRequestRequestChanges": `{"data":{"mergeRequestRequestChanges":{"errors":["Reviewer not found"]}}}`},
		answers: map[string]string{"git diff": fileDiff},
		fail:    map[string]string{"glab api user": "401 Unauthorized"},
	}
	_, err = NewGitLabWithRunner("/repo", f6.run).Submit(t.Context(), resolvedMR(), EventRequestChanges, annots)
	require.ErrorContains(t, err, "current user")
	require.ErrorContains(t, err, "notes are published")

	f3 := &fakeRunner{answers: map[string]string{"git diff": fileDiff}, fail: map[string]string{"glab api -X POST " + mrAPI + "/draft_notes --input -": "401 Unauthorized"}}
	_, err = NewGitLabWithRunner("/repo", f3.run).Submit(t.Context(), resolvedMR(), EventComment, annots)
	require.ErrorContains(t, err, "post draft note on pkg/a.go:12")

	f4 := &fakeRunner{answers: map[string]string{"git diff": fileDiff}, fail: map[string]string{"glab api -X POST " + mrAPI + "/approve": "409 Conflict"}}
	_, err = NewGitLabWithRunner("/repo", f4.run).Submit(t.Context(), resolvedMR(), EventApprove, annots)
	require.ErrorContains(t, err, "approve")
}

func TestGitLab_Comments(t *testing.T) {
	page1 := `[{"notes":[{"type":"DiffNote","body":" hi ","system":false,"resolved":false,"author":{"username":"ann"},"position":{"new_path":"pkg/a.go","old_path":"pkg/a.go","new_line":12,"old_line":null}}]}]`
	page2 := `[{"notes":[` +
		`{"type":"DiffNote","body":"old","author":{"username":"bob"},"position":{"new_path":"pkg/a.go","old_path":"pkg/old.go","new_line":null,"old_line":5}},` +
		`{"type":null,"body":"plain","author":{"username":"c"}},` +
		`{"type":"DiffNote","body":"done","resolved":true,"author":{"username":"d"},"position":{"new_path":"x","new_line":1}},` +
		`{"type":"DiffNote","body":"sys","system":true,"author":{"username":"e"},"position":{"new_path":"x","new_line":2}},` +
		`{"type":"DiffNote","body":"gone","author":{"username":"f"},"position":{"new_path":"x","new_line":null,"old_line":null}}` +
		`]}]`
	f := &fakeRunner{answers: map[string]string{"glab api --paginate " + mrAPI + "/discussions": page1 + "\n" + page2}}
	got, err := NewGitLabWithRunner("/repo", f.run).Comments(t.Context(), resolvedMR())
	require.NoError(t, err)
	assert.Equal(t, []LineComment{
		{Path: "pkg/a.go", Line: 12, Side: "RIGHT", Author: "ann", Body: "hi"},
		{Path: "pkg/old.go", Line: 5, Side: "LEFT", Author: "bob", Body: "old"},
	}, got, "every page is read, plain, resolved, system and unanchored notes are dropped")

	f2 := &fakeRunner{answers: map[string]string{"glab api --paginate": "not json"}}
	_, err = NewGitLabWithRunner("/repo", f2.run).Comments(t.Context(), resolvedMR())
	require.ErrorContains(t, err, "parse discussions")

	f3 := &fakeRunner{answers: map[string]string{"glab api --paginate": ""}}
	got, err = NewGitLabWithRunner("/repo", f3.run).Comments(t.Context(), resolvedMR())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestGitLab_ListOpen(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"glab mr list --per-page 100 -F json": `[{"iid":9,"title":" Nine ","source_branch":"nine","draft":true,"author":{"username":"ann"}},{"iid":8,"title":"Eight","source_branch":"eight","author":{"username":"bob"}}]`}}
	list, err := NewGitLabWithRunner("/repo", f.run).ListOpen(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []Summary{
		{Number: 9, Title: "Nine", Author: "ann", Branch: "nine", Draft: true},
		{Number: 8, Title: "Eight", Author: "bob", Branch: "eight"},
	}, list)

	f2 := &fakeRunner{answers: map[string]string{"glab mr list": ""}}
	list, err = NewGitLabWithRunner("/repo", f2.run).ListOpen(t.Context())
	require.NoError(t, err)
	assert.Nil(t, list)
}
