package gitops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yousysadmin/igit/internal/git"
)

// StageFiles adds paths to the index (`git add`). Untracked files become tracked.
func (g *Git) StageFiles(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := g.Run(ctx, git.RunOpts{}, append([]string{"add", "--"}, paths...)...)
	return err
}

// ResolveConflict settles an unmerged path by taking one side whole and
// marking it resolved. The checkout writes the chosen stage into the working
// tree, the add removes the path from the unmerged list.
func (g *Git) ResolveConflict(ctx context.Context, path string, ours bool) error {
	side := "--theirs"
	if ours {
		side = "--ours"
	}
	if _, err := g.Run(ctx, git.RunOpts{}, "checkout", side, "--", path); err != nil {
		return err
	}
	return g.StageFiles(ctx, []string{path})
}

// RestoreConflict puts one path's merge conflict back, undoing the staging
// that marked it resolved. A path that never conflicted is left alone, so it
// is safe to try before an ordinary unstage.
func (g *Git) RestoreConflict(ctx context.Context, path string) error {
	_, err := g.Run(ctx, git.RunOpts{}, "checkout", "-m", "--", path)
	return err
}

// hasUnmergedStages reports whether the index still holds the higher stages of
// a conflict for path.
func (g *Git) hasUnmergedStages(ctx context.Context, path string) bool {
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "ls-files", "-u", "--", path)
	return err == nil && strings.TrimSpace(out) != ""
}

// UnstageDuringMerge undoes the staging of one path while a merge, rebase or
// cherry-pick is in progress. A path that was a resolved conflict gets its
// conflict back, which is the real inverse of marking it resolved. Any other
// staged path is unstaged the ordinary way.
func (g *Git) UnstageDuringMerge(ctx context.Context, path string, unborn bool) error {
	if err := g.RestoreConflict(ctx, path); err == nil && g.hasUnmergedStages(ctx, path) {
		return nil
	}
	return g.UnstageFiles(ctx, []string{path}, unborn)
}

// StageAll stages every change including untracked files (`git add -A`).
func (g *Git) StageAll(ctx context.Context) error {
	_, err := g.Run(ctx, git.RunOpts{}, "add", "-A")
	return err
}

// UnstageFiles resets paths in the index to HEAD. On an unborn branch there is
// no HEAD to reset to, so the entries are removed from the index instead.
func (g *Git) UnstageFiles(ctx context.Context, paths []string, unborn bool) error {
	if len(paths) == 0 {
		return nil
	}
	if unborn {
		_, err := g.Run(ctx, git.RunOpts{}, append([]string{"rm", "--cached", "-q", "--force", "-r", "--"}, paths...)...)
		return err
	}
	_, err := g.Run(ctx, git.RunOpts{}, append([]string{"reset", "-q", "HEAD", "--"}, paths...)...)
	return err
}

// UnstageAll empties the index changes (`git reset`).
func (g *Git) UnstageAll(ctx context.Context, unborn bool) error {
	if unborn {
		_, err := g.Run(ctx, git.RunOpts{}, "rm", "--cached", "-q", "--force", "-r", "--", ".")
		return err
	}
	_, err := g.Run(ctx, git.RunOpts{}, "reset", "-q")
	return err
}

// DiscardScope selects how much of a file's changes Discard throws away.
type DiscardScope int

// Discard scopes.
const (
	DiscardUnstaged DiscardScope = iota // worktree changes only, the index is kept
	DiscardAll                          // index and worktree back to HEAD
)

// Discard reverts a file. Untracked files are deleted from disk. a file added
// to the index is removed from the index and, for DiscardAll, from disk. a
// staged rename is undone by restoring the original path. Tracked files are
// checked out from the index (DiscardUnstaged) or reset and checked out from
// HEAD (DiscardAll).
func (g *Git) Discard(ctx context.Context, f StatusEntry, scope DiscardScope, unborn bool) error {
	switch {
	case f.Untracked:
		return g.removeFromWorktree(f.Path)
	case f.IsNewInIndex() || (unborn && f.HasStaged()):
		if scope == DiscardUnstaged {
			// keep the index entry, drop the worktree edits on top of it
			_, err := g.Run(ctx, git.RunOpts{}, "checkout", "--", f.Path)
			return err
		}
		if err := g.UnstageFiles(ctx, []string{f.Path}, unborn); err != nil {
			return err
		}
		return g.removeFromWorktree(f.Path)
	case scope == DiscardUnstaged:
		_, err := g.Run(ctx, git.RunOpts{}, "checkout", "--", f.Path)
		return err
	}
	// DiscardAll on a tracked file: reset index to HEAD, then restore the worktree
	paths := []string{f.Path}
	if f.OrigPath != "" && f.OrigPath != f.Path {
		paths = append(paths, f.OrigPath)
	}
	if err := g.UnstageFiles(ctx, paths, unborn); err != nil {
		return err
	}
	if f.OrigPath != "" && f.OrigPath != f.Path {
		// the rename target does not exist in HEAD. delete it and restore the source
		if err := g.removeFromWorktree(f.Path); err != nil {
			return err
		}
		_, err := g.Run(ctx, git.RunOpts{}, "checkout", "--", f.OrigPath)
		return err
	}
	_, err := g.Run(ctx, git.RunOpts{}, "checkout", "--", f.Path)
	return err
}

// removeFromWorktree deletes path (file or directory) after checking that it
// stays inside the working tree.
func (g *Git) removeFromWorktree(path string) error {
	root, err := filepath.Abs(g.WorkDir())
	if err != nil {
		return fmt.Errorf("resolve worktree: %w", err)
	}
	abs := filepath.Join(root, path)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove %q: outside the working tree", path)
	}
	if err := os.RemoveAll(abs); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
