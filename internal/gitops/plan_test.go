package gitops

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/patch"
	"github.com/yousysadmin/igit/internal/stageplan"
)

// blockRange builds the plan range for the change block whose content matches
// wanted, from the file's current worktree diff (as the review pane would).
func blockRange(t *testing.T, g *Git, path string, wanted ...string) stageplan.Range {
	t.Helper()
	raw, err := g.StagingDiff(t.Context(), DiffSpec{Path: path})
	require.NoError(t, err)
	p, err := patch.Parse(raw)
	require.NoError(t, err)
	var block []git.DiffLine
	for _, v := range p.ViewLines() {
		for _, w := range wanted {
			if v.Content != w {
				continue
			}
			switch v.Kind {
			case patch.KindAddition:
				block = append(block, git.DiffLine{NewNum: v.NewNum, Content: v.Content, ChangeType: git.ChangeAdd})
			case patch.KindDeletion:
				block = append(block, git.DiffLine{OldNum: v.OldNum, Content: v.Content, ChangeType: git.ChangeRemove})
			case patch.KindContext, patch.KindHeader, patch.KindHunkHeader, patch.KindNoNewline:
			}
		}
	}
	require.NotEmpty(t, block)
	return stageplan.NewRange(block)
}

func TestApplyPlan_e2e(t *testing.T) {
	g := setupRepo(t)
	dir := g.WorkDir()
	ctx := t.Context()
	writeRepoFile(t, dir, "a.txt", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\n")
	writeRepoFile(t, dir, "whole.txt", "w\n")
	gitRun(t, dir, "add", "a.txt", "whole.txt")
	gitRun(t, dir, "commit", "-q", "-m", "base")
	writeRepoFile(t, dir, "a.txt", "L1\nl2\nl3\nl4\nl5\nl6\nMIDDLE\nl8\nl9\nl10\nl11\nl12\nL13\n")
	writeRepoFile(t, dir, "whole.txt", "W\nmore\n")
	writeRepoFile(t, dir, "new.txt", "n1\nn2\nn3\n")
	writeRepoFile(t, dir, "drift.txt", "d\n")

	plan := stageplan.New()
	plan.ToggleRange("a.txt", blockRange(t, g, "a.txt", "l7", "MIDDLE"))
	plan.ToggleRange("a.txt", blockRange(t, g, "a.txt", "L13"))
	plan.ToggleFile("whole.txt")
	// partial mark on an untracked file
	rawNew, err := g.StagingDiff(ctx, DiffSpec{Path: "new.txt", Untracked: true})
	require.NoError(t, err)
	pNew, err := patch.Parse(rawNew)
	require.NoError(t, err)
	var n2 []git.DiffLine
	for _, v := range pNew.ViewLines() {
		if v.Kind == patch.KindAddition && v.Content == "n2" {
			n2 = append(n2, git.DiffLine{NewNum: v.NewNum, Content: v.Content, ChangeType: git.ChangeAdd})
		}
	}
	plan.ToggleRange("new.txt", stageplan.NewRange(n2))
	// a mark whose content changed afterwards
	plan.ToggleRange("drift.txt", stageplan.NewRange([]git.DiffLine{{NewNum: 1, Content: "old d", ChangeType: git.ChangeAdd}}))
	// a whole-file mark on a path that does not exist
	plan.ToggleFile("missing.txt")

	res, err := g.ApplyPlan(ctx, plan)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt", "new.txt", "whole.txt"}, res.Staged)
	require.Len(t, res.Skipped, 2)
	assert.Equal(t, "drift.txt", res.Skipped[0].Path)
	assert.Contains(t, res.Skipped[0].Reason, "changed since it was marked")
	assert.Equal(t, "missing.txt", res.Skipped[1].Path)

	// exactly the two marked blocks of a.txt are in the index, the first block is not
	cached := gitOut(t, dir, "diff", "--cached", "--no-color", "-U0", "--", "a.txt")
	assert.Contains(t, cached, "-l7\n+MIDDLE\n")
	assert.Contains(t, cached, "+L13\n")
	assert.NotContains(t, cached, "+L1\n")
	assert.Equal(t, "W\nmore\n", gitOut(t, dir, "show", ":whole.txt"))
	assert.Equal(t, "n2\n", gitOut(t, dir, "show", ":new.txt"), "only the marked line of the new file is staged")
	st := statusMap(t, g)
	assert.True(t, st["drift.txt"].Untracked, "skipped file untouched")

	// empty and nil plans are no-ops
	res, err = g.ApplyPlan(ctx, stageplan.New())
	require.NoError(t, err)
	assert.Empty(t, res.Staged)
	_, err = g.ApplyPlan(ctx, nil)
	require.NoError(t, err)
}

func TestApplyPlan_statusError(t *testing.T) {
	g := New(t.TempDir())
	plan := stageplan.New()
	plan.ToggleFile("x")
	_, err := g.ApplyPlan(t.Context(), plan)
	require.Error(t, err)
}
