package session

import (
	"path/filepath"
	"strings"
	"time"
)

// outputTimeLayout is the timestamp embedded in annotation output file names.
// It matches the history file layout minus the millisecond suffix.
const outputTimeLayout = "2006-01-02T15-04-05"

// OutputFileName returns the file name for an annotation output written on
// exit without an explicit --output path: "<repo>-<timestamp>.md". The repo
// label is sanitized so it cannot introduce path separators or spaces. an
// empty label falls back to "review".
func OutputFileName(repo string, now time.Time) string {
	label := sanitizeLabel(repo)
	if label == "" {
		label = "review"
	}
	return label + "-" + now.Format(outputTimeLayout) + ".md"
}

// RepoLabel derives the short label identifying a review session, used for
// history subdirectories and output file names. A single --only file outside
// any repository uses its parent directory name, otherwise the base name of
// the working directory (or repository root) wins.
func RepoLabel(gitRoot, workDir string, only []string) string {
	if gitRoot == "" && len(only) == 1 {
		if abs, err := filepath.Abs(only[0]); err == nil {
			return filepath.Base(filepath.Dir(abs))
		}
	}
	path := workDir
	if path == "" {
		path = gitRoot
	}
	if path == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(path))
}

// sanitizeLabel replaces characters that are unsafe or awkward in file names.
func sanitizeLabel(s string) string {
	s = strings.TrimSpace(s)
	repl := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-")
	s = repl.Replace(s)
	return strings.Trim(s, "-.")
}
