package gitops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yousysadmin/igit/internal/git"
)

// defaultContextLines is the `-U` value used when DiffSpec.Context is not set.
const defaultContextLines = 3

// DiffSpec selects which diff of one file the staging view shows.
type DiffSpec struct {
	Path      string
	OrigPath  string // previous path of a rename (staged side only)
	Cached    bool   // index vs HEAD instead of worktree vs index
	Untracked bool   // file is not in the index: diff against /dev/null
	Context   int    // unified context lines, <= 0 means defaultContextLines
}

// RangeDiff returns the raw diff of a ref range ("A..B"), covering every file
// it touches. Used for the diff of a commit or a stash entry.
func (g *Git) RangeDiff(ctx context.Context, ref string, contextLines int) (string, error) {
	n := contextLines
	if n <= 0 {
		n = defaultContextLines
	}
	args := []string{"diff", "--no-ext-diff", "--no-color", "--no-textconv", "-M", "-U" + strconv.Itoa(n), ref}
	return g.Run(ctx, git.RunOpts{Background: true}, args...)
}

// StagingDiff returns the raw unified diff for spec. The text keeps every byte
// git printed (CRLF, trailing whitespace, "\ No newline" markers) so the patch
// package can re-emit selected lines for `git apply`.
func (g *Git) StagingDiff(ctx context.Context, spec DiffSpec) (string, error) {
	n := spec.Context
	if n <= 0 {
		n = defaultContextLines
	}
	args := []string{"diff", "--no-ext-diff", "--no-color", "--no-textconv", "-U" + strconv.Itoa(n)}
	switch {
	case spec.Untracked:
		// --no-index implies --exit-code, so a non-empty diff exits 1 and a missing
		// file also exits 1 with an empty diff. check for the file explicitly.
		if _, err := os.Stat(filepath.Join(g.WorkDir(), spec.Path)); err != nil {
			return "", fmt.Errorf("untracked file %s: %w", spec.Path, err)
		}
		args = append(args, "--no-index", "--", "/dev/null", spec.Path)
		return g.Run(ctx, git.RunOpts{Background: true, OkExitCodes: []int{1}}, args...)
	case spec.Cached:
		args = append(args, "--cached")
		if spec.OrigPath != "" && spec.OrigPath != spec.Path {
			args = append(args, "-M", "--", spec.OrigPath, spec.Path)
		} else {
			args = append(args, "--", spec.Path)
		}
	default:
		// an unmerged path has no plain worktree diff: git answers with a
		// combined diff the patch parser cannot read. --ours asks for the
		// worktree against our side instead, an ordinary diff carrying the
		// conflict markers. Every other path is unaffected by the flag.
		args = append(args, "--ours", "--", spec.Path)
	}
	out, err := g.Run(ctx, git.RunOpts{Background: true}, args...)
	if err != nil || !bareUnmergedNotice(out) {
		return out, err
	}
	// a modify/delete conflict has no blob on our side to diff against, so
	// --ours answers with the notice alone. The decision there is whether to
	// keep the file at all, which needs its content, not a line diff
	return g.wholeFileDiff(ctx, spec.Path)
}

// fileExists reports whether path names something on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// bareUnmergedNotice reports whether git only announced an unmerged path and
// produced no diff for it.
func bareUnmergedNotice(out string) bool {
	return strings.HasPrefix(out, "* Unmerged path") && !strings.Contains(out, "\n@@")
}

// wholeFileDiff renders the working-tree file as one addition. Returns an empty
// diff when the path is gone from the working tree.
func (g *Git) wholeFileDiff(ctx context.Context, path string) (string, error) {
	if !fileExists(filepath.Join(g.WorkDir(), path)) {
		return "", nil
	}
	// --no-index implies --exit-code, a non-empty diff exits 1
	return g.Run(ctx, git.RunOpts{Background: true, OkExitCodes: []int{1}},
		"diff", "--no-ext-diff", "--no-color", "--no-textconv", "--no-index", "--", "/dev/null", path)
}
