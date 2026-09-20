// Package gitops is the git write layer behind commit mode: working-tree
// status, staging and unstaging of files, hunks and lines, commits, stashes,
// branches, log and sync. Every invocation goes through the shared runner in
// internal/git, so the environment (literal pathspecs, no terminal prompts) is
// uniform across both modes and every failure surfaces as a *git.Error
// carrying the command's stderr.
//
// Portions follow the command lines used by lazygit (MIT, Jesse Duffield).
// see NOTICE.
package gitops

import (
	"sync"

	"github.com/yousysadmin/igit/internal/git"
)

// Git executes git write operations inside one working tree.
type Git struct {
	*git.Runner

	versionOnce sync.Once
	version     gitVersion
	versionErr  error
}

// New returns a Git bound to workDir (the repository root or any directory inside it).
func New(workDir string) *Git {
	return &Git{Runner: git.NewRunner(workDir)}
}
