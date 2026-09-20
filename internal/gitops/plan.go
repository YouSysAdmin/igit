package gitops

import (
	"context"
	"errors"
	"fmt"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/patch"
	"github.com/yousysadmin/igit/internal/stageplan"
)

// ApplyPlan stages a review stage plan: whole-file marks become `git add`,
// range marks become partial patches applied to the index. A file whose diff
// changed since it was marked (hash mismatch) or whose apply fails is reported
// in Result.Skipped and left untouched. the other files still go through. The
// returned error covers only failures that stop the whole run, such as reading
// the status.
func (g *Git) ApplyPlan(ctx context.Context, plan *stageplan.Plan) (stageplan.Result, error) {
	var res stageplan.Result
	if plan == nil || plan.Empty() {
		return res, nil
	}
	st, err := g.Status(ctx)
	if err != nil {
		return res, err
	}
	untracked := map[string]bool{}
	for _, f := range st.Files {
		if f.Untracked {
			untracked[f.Path] = true
		}
	}
	for _, path := range plan.Files() {
		marks, _ := plan.Marks(path)
		if marks.Whole {
			if err := g.StageFiles(ctx, []string{path}); err != nil {
				res.Skipped = append(res.Skipped, stageplan.Skipped{Path: path, Reason: err.Error()})
				continue
			}
			res.Staged = append(res.Staged, path)
			continue
		}
		if err := g.applyRanges(ctx, path, marks.Ranges, untracked[path]); err != nil {
			res.Skipped = append(res.Skipped, stageplan.Skipped{Path: path, Reason: err.Error()})
			continue
		}
		res.Staged = append(res.Staged, path)
	}
	return res, nil
}

// errPlanDrift is reported when a marked block no longer matches the diff.
var errPlanDrift = errors.New("diff changed since it was marked")

// applyRanges stages the marked ranges of one file from its current worktree diff.
func (g *Git) applyRanges(ctx context.Context, path string, ranges []stageplan.Range, isUntracked bool) error {
	raw, err := g.StagingDiff(ctx, DiffSpec{Path: path, Untracked: isUntracked})
	if err != nil {
		return err
	}
	p, err := patch.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse diff of %s: %w", path, err)
	}
	var indices []int
	for _, r := range ranges {
		idx, changes := selectRange(p, r)
		if len(idx) == 0 || stageplan.HashLines(changes) != r.Hash {
			return errPlanDrift
		}
		indices = append(indices, idx...)
	}
	text := p.Transform(patch.TransformOpts{FileNameOverride: path, IncludedLineIndices: indices}).FormatPlain()
	if text == "" {
		return ErrNoChangesSelected
	}
	return g.ApplyPatch(ctx, text, ApplyOpts{Cached: true})
}

// selectRange returns the patch line indices of the changes inside r, plus
// the same lines as DiffLines for hashing.
func selectRange(p *patch.Patch, r stageplan.Range) (indices []int, changes []git.DiffLine) {
	for _, v := range p.ViewLines() {
		switch v.Kind {
		case patch.KindAddition:
			if r.Covers(0, v.NewNum) {
				indices = append(indices, v.PatchIdx)
				changes = append(changes, git.DiffLine{NewNum: v.NewNum, Content: v.Content, ChangeType: git.ChangeAdd})
			}
		case patch.KindDeletion:
			if r.Covers(v.OldNum, 0) {
				indices = append(indices, v.PatchIdx)
				changes = append(changes, git.DiffLine{OldNum: v.OldNum, Content: v.Content, ChangeType: git.ChangeRemove})
			}
		case patch.KindContext, patch.KindHeader, patch.KindHunkHeader, patch.KindNoNewline:
		}
	}
	return indices, changes
}
