package gitops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/patch"
)

// ApplyOpts tunes `git apply`.
type ApplyOpts struct {
	Cached   bool // apply to the index only
	Reverse  bool // apply the patch backwards
	ThreeWay bool // fall back to a three-way merge on conflicts
}

// ApplyPatch feeds patchText to `git apply` on stdin. Nothing touches the disk
// besides the index or worktree git updates.
func (g *Git) ApplyPatch(ctx context.Context, patchText string, o ApplyOpts) error {
	args := []string{"apply", "--whitespace=nowarn"}
	if o.Cached {
		args = append(args, "--cached")
	}
	if o.Reverse {
		args = append(args, "--reverse")
	}
	if o.ThreeWay {
		args = append(args, "--3way")
	}
	args = append(args, "-")
	_, err := g.Run(ctx, git.RunOpts{Stdin: strings.NewReader(patchText)}, args...)
	return err
}

// LineOp is what to do with a selection of diff lines.
type LineOp int

// Line operations. StageLines and UnstageLines move lines between the
// worktree diff and the index. DiscardLines reverts worktree lines.
const (
	StageLines LineOp = iota
	UnstageLines
	DiscardLines
)

// LineRequest selects lines of one file's raw diff (see StagingDiff).
type LineRequest struct {
	Raw     string // the diff the indices refer to
	Path    string // file path used in the rewritten patch header
	Indices []int  // patch line indices (patch.Patch.Lines) to include
	Op      LineOp
}

// ErrNoChangesSelected is returned when the selection holds no addition or
// deletion, so there is nothing to apply.
var ErrNoChangesSelected = errors.New("no changed lines selected")

// ApplyLines builds a partial patch from req and applies it: staging applies
// to the index, unstaging applies the reverse to the index, discarding
// applies the reverse to the worktree.
func (g *Git) ApplyLines(ctx context.Context, req LineRequest) error {
	p, err := patch.Parse(req.Raw)
	if err != nil {
		return fmt.Errorf("parse diff of %s: %w", req.Path, err)
	}
	reverse := req.Op != StageLines
	text := p.Transform(patch.TransformOpts{
		Reverse:             reverse,
		FileNameOverride:    req.Path,
		IncludedLineIndices: req.Indices,
	}).FormatPlain()
	if text == "" {
		return ErrNoChangesSelected
	}
	return g.ApplyPatch(ctx, text, ApplyOpts{Cached: req.Op != DiscardLines, Reverse: reverse})
}

// HunkIndices returns the patch line indices covering the whole hunk that
// contains line idx of raw, for hunk-level operations. ok is false when idx is
// not inside a hunk.
func HunkIndices(raw string, idx int) (indices []int, ok bool) {
	p, err := patch.Parse(raw)
	if err != nil {
		return nil, false
	}
	h := p.HunkContainingLine(idx)
	if h < 0 {
		return nil, false
	}
	return patch.ExpandRange(p.HunkStartIdx(h), p.HunkEndIdx(h)), true
}
