package session

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yousysadmin/igit/internal/annot"
)

// Entry is one saved review in the history directory: the header fields Save
// wrote plus the parsed annotation records.
type Entry struct {
	File    string    // history file path
	Scope   string    // file name without extension: the review scope (see ScopeName)
	Time    time.Time // when the entry was last written (from the title line)
	Path    string    // repository or file path the review ran in
	Ref     string    // ref range, "" for the working tree
	Commit  string    // short HEAD hash at save time, "" when unknown
	PR      int       // pull request number, 0 when the session was not a PR review
	Records []annot.Annotation

	annotations string // raw annotations section, compared on Save to skip unchanged rewrites
}

// headerTimeLayout is the stamp in the "# Review:" title line.
const headerTimeLayout = "2006-01-02 15:04:05"

// annotationsMarker and diffMarker delimit the annotations section of a
// history file. everything after diffMarker is the diff snapshot.
const (
	annotationsMarker = "## Annotations\n"
	diffMarker        = "\n---\n\n## Diff\n"
)

// List returns the saved reviews of the repository directory that Params
// selects (the same directory Save writes to), newest first. Unreadable
// entries are skipped. a missing directory yields no entries and no error.
func (s *Service) List(p Params) ([]Entry, error) {
	dir := s.historyDir(p)
	if dir == "" {
		return nil, errors.New("history: cannot determine history directory")
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("history: list %s: %w", dir, err)
	}
	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		e, rerr := ReadEntry(name)
		if rerr != nil {
			continue
		}
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		// newest first, then by scope
		if c := b.Time.Compare(a.Time); c != 0 {
			return c
		}
		return cmp.Compare(a.Scope, b.Scope)
	})
	return entries, nil
}

// ReadEntry parses one history file: the header lines up to the annotations
// section, then the annotation records between the section marker and the
// diff snapshot.
func ReadEntry(file string) (Entry, error) {
	data, err := os.ReadFile(file) //nolint:gosec // history files live under the user's own config directory
	if err != nil {
		return Entry{}, fmt.Errorf("history: read %s: %w", file, err)
	}
	text := string(data)
	e := Entry{File: file, Scope: strings.TrimSuffix(filepath.Base(file), ".md")}
	if info, serr := os.Stat(file); serr == nil {
		e.Time = info.ModTime()
	}
	head, rest, ok := strings.Cut(text, annotationsMarker)
	if !ok {
		return Entry{}, fmt.Errorf("history: %s has no annotations section", file)
	}
	e.parseHeader(head)
	body, _, _ := strings.Cut(rest, diffMarker)
	e.annotations = strings.TrimPrefix(body, "\n")
	records, err := annot.Parse(strings.NewReader(body))
	if err != nil {
		return Entry{}, fmt.Errorf("history: %s: %w", file, err)
	}
	e.Records = records
	return e, nil
}

// parseHeader reads the "key: value" lines Save writes under the title.
func (e *Entry) parseHeader(head string) {
	sc := bufio.NewScanner(strings.NewReader(head))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ": ")
		if !ok {
			continue
		}
		switch key {
		case "# Review":
			if t, perr := time.ParseInLocation(headerTimeLayout, value, time.Local); perr == nil {
				e.Time = t
			}
		case "path":
			e.Path = value
		case "refs":
			e.Ref = value
		case "commit":
			e.Commit = value
		case "pr":
			e.PR, _ = strconv.Atoi(value)
		}
	}
}

// Label describes the entry in one line for pickers and messages.
func (e Entry) Label() string {
	scope := e.Ref
	switch {
	case e.PR > 0:
		scope = fmt.Sprintf("PR #%d", e.PR)
	case scope == "":
		scope = "working tree"
	}
	n := len(e.Records)
	noun := "annotations"
	if n == 1 {
		noun = "annotation"
	}
	return fmt.Sprintf("%s  %s  %d %s", e.Time.Format("2006-01-02 15:04"), scope, n, noun)
}

// HeadCommit returns the short hash of HEAD in gitRoot, "" when unavailable.
func HeadCommit(gitRoot string) string { return (&Service{}).gitCommitHash(gitRoot) }
