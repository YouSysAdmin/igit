package forge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
)

func TestPullRequest_kindWords(t *testing.T) {
	pr := PullRequest{Number: 12, Title: "Fix"}
	assert.Equal(t, "PR #12", pr.Name())
	assert.Equal(t, "PR #12: Fix", pr.Label())
	assert.Equal(t, "pr", pr.ScopeKind())
	assert.Equal(t, "pull request", pr.Noun())

	mr := PullRequest{Forge: KindGitLab, Number: 12, Title: "Fix"}
	assert.Equal(t, "MR !12", mr.Name())
	assert.Equal(t, "MR !12: Fix", mr.Label())
	assert.Equal(t, "mr", mr.ScopeKind())
	assert.Equal(t, "merge request", mr.Noun())
	assert.Equal(t, "MR !12", PullRequest{Forge: KindGitLab, Number: 12}.Label(), "no title, no colon")
}

func TestAnchor_pairsContextLines(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"git diff": fileDiff}}
	pr := PullRequest{MergeBase: "mb", HeadSHA: "head"}
	annots := []annot.Annotation{
		{File: "pkg/a.go", Line: 12, Type: "+", Comment: "added"},
		{File: "pkg/a.go", Line: 14, Type: " ", Comment: "context"},
		{File: "pkg/a.go", Line: 0, Comment: "whole file"},
	}
	plan, diffs, err := anchor(t.Context(), f.run, "/repo", pr, annots)
	require.NoError(t, err)
	require.Len(t, plan.Comments, 2)
	require.Len(t, plan.Outside, 1)
	dl := diffs["pkg/a.go"]
	require.NotNil(t, dl)
	assert.Equal(t, 13, dl.oldOfNew[14], "ctx3 is old 13, new 14")
	_, added := dl.oldOfNew[12]
	assert.False(t, added, "an added line has no old number")
	assert.Len(t, f.calls, 1, "one diff per file")
}
