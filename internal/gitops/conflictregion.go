package gitops

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yousysadmin/igit/internal/conflict"
)

// ConflictRegions lists the merge blocks left in a working-tree file, in file
// order. An empty list means the file carries no markers, which is what a
// fully resolved path looks like before it is staged.
func (g *Git) ConflictRegions(path string) ([]conflict.Region, error) {
	body, err := g.readWorktreeFile(path)
	if err != nil {
		return nil, err
	}
	return conflict.Parse(body), nil
}

// ResolveConflictRegion rewrites one merge block of a working-tree file to the
// chosen side, markers and all. The other blocks are left alone, so a file can
// be settled one conflict at a time. The path stays unmerged until it is
// staged, which is what marks it resolved for git.
func (g *Git) ResolveConflictRegion(path string, index int, choice conflict.Choice) error {
	full := filepath.Join(g.WorkDir(), path)
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	body, err := g.readWorktreeFile(path)
	if err != nil {
		return err
	}
	next, err := conflict.Resolve(body, index, choice)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	// the file keeps its mode, an executable script must stay executable
	if err := os.WriteFile(full, []byte(next), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// readWorktreeFile reads a path inside the working tree.
func (g *Git) readWorktreeFile(path string) (string, error) {
	body, err := os.ReadFile(filepath.Join(g.WorkDir(), path)) //nolint:gosec // path comes from git status, inside the working tree
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(body), nil
}
