package git

import (
	"os"
	"path/filepath"
)

// DiscoverRoot walks up from startDir looking for a repository and returns its
// root. .git may be a file in worktrees and submodules, so any entry with that
// name counts. ok is false when there is no repository above startDir, which is
// the standalone-file session (--only) and the stdin diff.
func DiscoverRoot(startDir string) (root string, ok bool) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
