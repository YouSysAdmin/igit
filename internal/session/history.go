package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yousysadmin/igit/internal/fsutil"
	"github.com/yousysadmin/igit/internal/git"
)

// Params holds parameters for saving a review history entry.
type Params struct {
	Annotations    string   // formatted annotations from Store.FormatOutput()
	Path           string   // full repo or file path
	Ref            string   // git ref (e.g. "master..HEAD") or empty
	Staged         bool     // whether --staged was used
	GitRoot        string   // git repo root, empty when git unavailable
	AnnotatedFiles []string // files with annotations, from Store.Files()
	SubDir         string   // history subdirectory name override, when empty, derived from Path
	PR             int      // pull or merge request number for a request review, 0 otherwise
	PRKind         string   // history scope prefix of that request, "pr" or "mr", empty means "pr"
	Commit         string   // commit the review is against, empty means the repository HEAD
}

// Service manages review history persistence: one entry per review scope
// (working tree, ref range or pull request) inside a per-repository directory.
type Service struct {
	baseDir    string // base history directory, empty = default (~/.config/igit/history/)
	MaxEntries int    // keep at most this many entries per repository directory, 0 = unlimited
}

// New creates a history service with the given base directory.
// if baseDir is empty, defaults to ~/.config/igit/history/.
func New(baseDir string) *Service {
	return &Service{baseDir: baseDir}
}

// Save stores the review of p's scope: the file is rewritten when the
// annotations changed, left alone when they did not, and removed when the
// session ended with no annotations. Older entries beyond MaxEntries are
// pruned. Errors are logged, never returned: this is a safety net that must
// not fail the process.
func (s *Service) Save(p Params) {
	dir := s.historyDir(p)
	if dir == "" {
		log.Printf("[WARN] history: cannot determine history directory")
		return
	}
	fname := filepath.Join(dir, ScopeName(p)+".md")
	if p.Annotations == "" {
		if err := os.Remove(fname); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[WARN] history: remove %s: %v", fname, err)
		}
		return
	}
	if prev, err := ReadEntry(fname); err == nil && prev.annotations == p.Annotations {
		return // nothing new to record
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("[WARN] history: create directory %s: %v", dir, err)
		return
	}

	now := time.Now()
	var buf strings.Builder
	fmt.Fprintf(&buf, "# Review: %s\n", now.Format(headerTimeLayout))
	fmt.Fprintf(&buf, "path: %s\n", p.Path)
	if p.Ref != "" {
		fmt.Fprintf(&buf, "refs: %s\n", p.Ref)
	}
	if p.PR > 0 {
		fmt.Fprintf(&buf, "pr: %d\n", p.PR)
	}
	if hash := s.reviewCommit(p); hash != "" {
		fmt.Fprintf(&buf, "commit: %s\n", hash)
	}

	buf.WriteString("\n" + annotationsMarker + "\n")
	buf.WriteString(p.Annotations)

	if diffOut := s.gitDiff(p); diffOut != "" {
		buf.WriteString(diffMarker + "\n")
		buf.WriteString(diffOut)
		if !strings.HasSuffix(diffOut, "\n") {
			buf.WriteString("\n")
		}
	}

	if err := fsutil.AtomicWriteFile(fname, []byte(buf.String())); err != nil {
		log.Printf("[WARN] history: write %s: %v", fname, err)
		return
	}
	s.prune(dir, fname)
}

// ScopeName is the file name (without extension) of a scope's history entry:
// "pr-N" for a pull request, "mr-N" for a merge request, "worktree" or
// "worktree-staged" for the working
// tree, otherwise the ref range with path separators and other unsafe
// characters replaced.
func ScopeName(p Params) string {
	switch {
	case p.PR > 0:
		return fmt.Sprintf("%s-%d", cmp.Or(p.PRKind, "pr"), p.PR)
	case p.Ref == "" && p.Staged:
		return "worktree-staged"
	case p.Ref == "":
		return "worktree"
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == '~', r == '^':
			return r
		}
		return '_'
	}, p.Ref)
	if len(name) > 80 {
		name = name[:80]
	}
	return "ref-" + name
}

// prune removes the oldest entries of dir beyond MaxEntries, never the one
// just written.
func (s *Service) prune(dir, keep string) {
	if s.MaxEntries <= 0 {
		return
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(names) <= s.MaxEntries {
		return
	}
	type aged struct {
		name string
		mod  time.Time
	}
	files := make([]aged, 0, len(names))
	for _, n := range names {
		if n == keep {
			continue
		}
		info, statErr := os.Stat(n)
		if statErr != nil {
			continue
		}
		files = append(files, aged{n, info.ModTime()})
	}
	slices.SortFunc(files, func(a, b aged) int { return a.mod.Compare(b.mod) })
	excess := len(names) - s.MaxEntries
	for i := range min(excess, len(files)) {
		if err := os.Remove(files[i].name); err != nil {
			log.Printf("[WARN] history: prune %s: %v", files[i].name, err)
		}
	}
}

// historyDir returns the directory for saving history files.
// uses s.baseDir if set, otherwise ~/.config/igit/history/, with repo basename appended.
func (s *Service) historyDir(p Params) string {
	base := s.baseDir
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config", "igit", "history")
	}

	subdir := "unknown"
	switch {
	case p.SubDir != "":
		subdir = p.SubDir
	case p.Path != "":
		subdir = filepath.Base(p.Path)
	}
	return filepath.Join(base, subdir)
}

// gitDiff runs git diff for annotated files and returns the raw output.
// returns empty string if git is unavailable or on error.
// files outside the git repo are filtered out to prevent git from failing
// with "is outside repository" error, which would lose the diff for all files.
func (s *Service) gitDiff(p Params) string {
	if p.GitRoot == "" || len(p.AnnotatedFiles) == 0 {
		return ""
	}

	repoFiles := s.filterRepoFiles(p.GitRoot, p.AnnotatedFiles)
	if len(repoFiles) == 0 {
		return ""
	}

	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if p.Staged {
		args = append(args, "--cached")
	}
	if p.Ref != "" {
		args = append(args, p.Ref)
	}
	args = append(args, "--")
	args = append(args, repoFiles...)

	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = p.GitRoot
	// annotated file names come from the working tree, so a file actually named
	// ":(top)x" must select itself instead of being parsed as a pathspec expression
	cmd.Env = git.GitEnv()
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && len(exitErr.Stderr) > 0 {
			log.Printf("[WARN] history: git diff: %s", strings.TrimSpace(string(exitErr.Stderr)))
		} else {
			log.Printf("[WARN] history: git diff: %v", err)
		}
		return ""
	}
	return string(out)
}

// reviewCommit returns the commit the review is against: the one the caller
// named, otherwise the repository HEAD.
func (s *Service) reviewCommit(p Params) string {
	if p.Commit != "" {
		return p.Commit
	}
	return s.gitCommitHash(p.GitRoot)
}

// gitCommitHash returns the short commit hash from the git root.
// returns empty string if git is unavailable or on error.
func (s *Service) gitCommitHash(gitRoot string) string {
	if gitRoot == "" {
		return ""
	}
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--short", "HEAD")
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// filterRepoFiles returns only files that are inside the git repo root,
// canonicalized to repo-relative paths. This ensures git receives clean pathspecs
// regardless of original path format (absolute, relative with .., etc).
func (s *Service) filterRepoFiles(gitRoot string, files []string) []string {
	result := make([]string, 0, len(files))
	for _, f := range files {
		absPath := f
		if !filepath.IsAbs(f) {
			absPath = filepath.Join(gitRoot, f)
		}
		rel, err := filepath.Rel(gitRoot, absPath)
		if err != nil || !filepath.IsLocal(rel) {
			continue // outside repo
		}
		result = append(result, rel)
	}
	return result
}
