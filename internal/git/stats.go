package git

import (
	"os"
	"path/filepath"
	"strings"
)

// maxUntrackedBytes bounds the per-file size for untracked-content reads
// during stats computation. The popup only needs +/- counts, so loading a
// multi-GB log/dump that happens to be untracked would balloon memory and
// block the goroutine for no user-visible payoff. Files exceeding the cap
// are skipped and the stats result is marked partial.
const maxUntrackedBytes = 16 * 1024 * 1024

// FileDiffer is the minimal subset of a diff renderer that stats computation
// needs. Defined consumer-side so tests can supply mocks without importing
// the full tui.DiffSource surface.
type FileDiffer interface {
	FileDiff(req FileDiffRequest) ([]DiffLine, error)
}

// Stats holds the aggregate add/remove counts for a review and whether one
// or more per-file fallbacks failed. Err is set when a primary FileDiff call
// returned an error. the caller stops aggregating in that case so the UI can
// surface the message instead of reporting silently-zero totals.
type Stats struct {
	Adds    int
	Removes int
	Partial bool
	Err     error
}

// StatsRequest carries the inputs ComputeStats needs to walk a review's file
// list and aggregate +/- counts. Bundled into a struct because the prior
// 5-positional shape (differ, ref, staged, workDir, entries) hit the
// project's "4+ params -> option struct" rule and made the call site harder
// to skim.
type StatsRequest struct {
	Differ  FileDiffer
	Ref     string
	Staged  bool
	WorkDir string
	Entries []FileEntry
}

// workDirRoots holds the original workDir alongside its symlink-resolved
// twin so callers in hot loops resolve EvalSymlinks(workDir) once and pass
// the pair around. Bundled as a struct because the "two same-typed paths"
// shape was the silent-swap risk flagged in review.
type workDirRoots struct {
	workDir     string // original (un-resolved) workDir
	realWorkDir string // filepath.EvalSymlinks(workDir), "" when workDir is empty or unresolvable
}

// resolveWorkDir returns filepath.EvalSymlinks(workDir) or "" when workDir
// is empty or cannot be resolved. Called once per ComputeStats invocation
// (hot loops never call this - see workDirRoots) and from tests that exercise
// safeWorkDirPath through the same call shape ComputeStats uses.
func resolveWorkDir(workDir string) string {
	if workDir == "" {
		return ""
	}
	r, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		return ""
	}
	return r
}

// ComputeStats aggregates the added and removed lines of req.Entries for the
// popup footer. An added file with an empty diff is re-fetched as staged, an
// untracked file is read from disk through safeWorkDirPath. The first
// FileDiff error stops the walk and is returned in Err.
func ComputeStats(req StatsRequest) Stats {
	var stats Stats
	// Resolve workDir symlinks once instead of per untracked entry. each call
	// to EvalSymlinks walks every path component, so hoisting this out of the
	// loop is the difference between O(N) and O(1) FS work for the workDir
	// side of the path-safety check.
	roots := workDirRoots{workDir: req.WorkDir, realWorkDir: resolveWorkDir(req.WorkDir)}
	// contextLines=0 requests full-file context, which skips the per-file
	// totalOldLines probe inside the diff source - that probe fires a
	// separate `git show <ref>:<file>` per file solely to
	// emit the trailing divider, and stats only consumes CountChanges so
	// the divider would be discarded anyway.
	for _, e := range req.Entries {
		if e.Status == FileUntracked {
			lines, partial := readUntracked(roots, e.Path, stats.Partial)
			stats.Partial = partial
			adds, removes := CountChanges(lines)
			stats.Adds += adds
			stats.Removes += removes
			continue
		}
		lines, err := req.Differ.FileDiff(FileDiffRequest{Ref: req.Ref, Path: e.Path, OldPath: e.OldPath, Staged: req.Staged})
		if err != nil {
			stats.Err = err
			return stats
		}
		if len(lines) == 0 && !req.Staged && e.Status == FileAdded {
			cached, cachedErr := req.Differ.FileDiff(FileDiffRequest{Ref: req.Ref, Path: e.Path, OldPath: e.OldPath, Staged: true})
			switch {
			case cachedErr != nil:
				stats.Partial = true
			case len(cached) > 0:
				lines = cached
			}
		}
		adds, removes := CountChanges(lines)
		stats.Adds += adds
		stats.Removes += removes
	}
	return stats
}

// readUntracked reads an untracked file for the stats footer and returns its
// lines together with the partial flag, which is raised whenever the file was
// not fully accounted: an unsafe path, a read error, a non-regular or
// oversized file, or a binary placeholder.
func readUntracked(roots workDirRoots, relPath string, prevPartial bool) ([]DiffLine, bool) {
	path, ok := safeWorkDirPath(roots.workDir, roots.realWorkDir, relPath)
	if !ok {
		return nil, true
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, true
	}
	if !info.Mode().IsRegular() {
		return nil, true
	}
	if info.Size() > maxUntrackedBytes {
		return nil, true
	}
	lines, err := ReadFileAsAdded(path)
	if err != nil {
		return nil, true
	}
	if isBinaryPlaceholderLines(lines) {
		// the file is binary or otherwise unreadable as text. ReadFileAsAdded
		// emits a placeholder but no add/remove signal. Counting it as zero
		// would silently drop it from the footer's total - flag partial.
		return nil, true
	}
	return lines, prevPartial
}

// isBinaryPlaceholderLines reports whether ReadFileAsAdded returned a
// single placeholder row (binary file, broken symlink, non-regular target,
// over-long line). Such rows carry IsBinary or IsPlaceholder. the row keeps
// its ChangeContext type so it contributes 0/0 to CountChanges, which would
// silently mark the file as fully accounted. Flagging Partial here keeps
// the footer honest.
func isBinaryPlaceholderLines(lines []DiffLine) bool {
	if len(lines) != 1 {
		return false
	}
	return lines[0].IsBinary || lines[0].IsPlaceholder
}

// safeWorkDirPath joins workDir and relPath and reports whether the result
// stays inside the working tree. realWorkDir is a pre-resolved
// filepath.EvalSymlinks(workDir) so callers in a loop resolve it once.
// Rejected: an empty directory, an absolute relPath, a lexical ".." escape, a
// symlink pointing outside, and a path that does not resolve. The check is
// lexical plus symlink resolution, not TOCTOU-proof: the caller re-opens the
// returned path afterwards.
func safeWorkDirPath(workDir, realWorkDir, relPath string) (string, bool) {
	if workDir == "" || realWorkDir == "" || filepath.IsAbs(relPath) {
		return "", false
	}
	full := filepath.Join(workDir, relPath)
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(realWorkDir, realFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}
