package main

import (
	"errors"
	"os"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/tui"
)

// diffSetup is what the composition root needs from the repository discovery:
// the diff source the review model reads, the roots it resolves paths against,
// and the optional git-only capabilities. isGit is false for a standalone-file
// session, where every git-only field stays nil.
type diffSetup struct {
	source             tui.DiffSource
	isGit              bool
	gitRoot            string // repository root, empty for standalone files
	workDir            string
	blamer             tui.Blamer
	untrackedFn        func() ([]string, error)
	untrackedRenamesFn func([]string) ([]git.FileEntry, error) // pairs untracked renames with their origin
	commitLogger       git.CommitLogger                        // commit log source, nil for standalone files
}

// setupDiffSource finds the repository and builds the diff source, blamer and
// untracked function for it, falling back to the standalone-file reader.
func setupDiffSource(opts options) (diffSetup, error) {
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		cwd = "."
	}
	root, ok := git.DiscoverRoot(cwd)
	if !ok {
		r, workDir, err := fileDiffSource(opts.Review.Only, cwd)
		if err != nil {
			return diffSetup{}, err
		}
		return diffSetup{source: r, workDir: workDir}, nil
	}
	g := git.NewGit(root)
	r, workDir, err := gitDiffSource(g, opts, root)
	if err != nil {
		return diffSetup{}, err
	}
	return diffSetup{
		source: r, isGit: true, gitRoot: root, workDir: workDir, blamer: g,
		untrackedFn: g.UntrackedFiles, untrackedRenamesFn: g.UntrackedRenames, commitLogger: g,
	}, nil
}

// gitDiffSource selects the git diff source for the current flags, reusing the
// provided *Git as the default so it is not allocated twice.
func gitDiffSource(g *git.Git, opts options, repoRoot string) (tui.DiffSource, string, error) { //nolint:unparam // error kept for symmetry with fileDiffSource
	var r tui.DiffSource
	switch {
	case len(opts.Review.Only) > 0:
		r = git.NewFallbackSource(g, opts.Review.Only, repoRoot)
	default:
		r = g
	}
	return wrapFilters(r, opts), repoRoot, nil
}

// wrapFilters applies include/exclude filters to a diff source based on opts.
func wrapFilters(r tui.DiffSource, opts options) tui.DiffSource {
	if len(opts.Include) > 0 {
		r = git.NewIncludeFilter(r, opts.Include)
	}
	if len(opts.Exclude) > 0 {
		r = git.NewExcludeFilter(r, opts.Exclude)
	}
	return r
}

// filterUntracked wraps an untracked-files function so its results honor the
// --include / --exclude prefixes. The raw UntrackedFiles call bypasses the
// source's IncludeFilter/ExcludeFilter (those only filter ChangedFiles), so
// without this wrap untracked files leak into a scoped review. Returns fn
// unchanged when fn is nil or no prefixes are set.
func filterUntracked(fn func() ([]string, error), include, exclude []string) func() ([]string, error) {
	if fn == nil || (len(include) == 0 && len(exclude) == 0) {
		return fn
	}
	return func() ([]string, error) {
		paths, err := fn()
		if err != nil {
			return nil, err
		}
		return git.FilterPaths(paths, include, exclude), nil
	}
}

// fileDiffSource creates a diff source when there is no repository.
// Standalone mode requires --only, which is mutually exclusive with --include.
// --exclude is a no-op here (FileReader only returns the --only files).
func fileDiffSource(only []string, cwd string) (tui.DiffSource, string, error) {
	if len(only) == 0 {
		return nil, "", errors.New("no git repository found (use --only to review standalone files)")
	}
	return git.NewFileReader(only, cwd), cwd, nil
}
