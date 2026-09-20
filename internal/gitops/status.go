package gitops

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yousysadmin/igit/internal/git"
)

// ChangeCode is one column of the porcelain XY status: '.' unmodified, 'M'
// modified, 'T' type changed, 'A' added, 'D' deleted, 'R' renamed, 'C' copied,
// 'U' unmerged. Untracked files carry '?' in both columns.
type ChangeCode byte

// Unmodified is the porcelain v2 code for "no change" in a column.
const Unmodified ChangeCode = '.'

// StatusEntry is one entry of `git status`.
type StatusEntry struct {
	Path        string
	OrigPath    string     // previous path for renames and copies
	Index       ChangeCode // X: index vs HEAD
	Worktree    ChangeCode // Y: worktree vs index
	Untracked   bool
	Ignored     bool
	Conflict    bool   // unmerged entry
	ConflictXY  string // "UU", "AA", "DD", "AU", "UA", "DU", "UD" for conflicts
	Submodule   bool
	RenameScore int // similarity percentage for renames/copies
}

// HasStaged reports whether the index differs from HEAD for this entry.
func (f StatusEntry) HasStaged() bool {
	return !f.Untracked && !f.Ignored && !f.Conflict && f.Index.changed()
}

// HasUnstaged reports whether the worktree differs from the index (or the file
// is untracked or in conflict, both of which need worktree-side action).
func (f StatusEntry) HasUnstaged() bool {
	if f.Ignored {
		return false
	}
	return f.Untracked || f.Conflict || f.Worktree.changed()
}

// changed reports whether the code denotes a modification. both the porcelain
// "." and the zero value mean unmodified.
func (c ChangeCode) changed() bool { return c != Unmodified && c != 0 }

// IsNewInIndex reports whether the entry was added to the index and has no
// HEAD version, so unstaging means `git rm --cached` instead of `git reset`.
func (f StatusEntry) IsNewInIndex() bool {
	return f.Index == 'A'
}

// ShortStatus returns the two-letter XY code as `git status --short` prints it.
func (f StatusEntry) ShortStatus() string {
	switch {
	case f.Untracked:
		return "??"
	case f.Ignored:
		return "!!"
	case f.Conflict:
		return f.ConflictXY
	}
	return string(f.Index.short()) + string(f.Worktree.short())
}

func (c ChangeCode) short() byte {
	if !c.changed() {
		return ' '
	}
	return byte(c)
}

// BranchHead is the `--branch` header of porcelain v2 status.
type BranchHead struct {
	Name         string // branch name, "" when detached
	OID          string // HEAD commit, "" on an unborn branch
	Upstream     string // "origin/main", "" when no upstream is configured
	Ahead        int
	Behind       int
	Detached     bool
	Unborn       bool
	UpstreamGone bool // upstream configured but its ref no longer exists
}

// Status is a full `git status` snapshot.
type Status struct {
	Head       BranchHead
	StashCount int
	Files      []StatusEntry
	// InProgress names a merge, rebase, cherry-pick, revert or bisect the
	// working tree is in the middle of. Porcelain status does not report one,
	// it is read from the sequencer files.
	InProgress InProgress
}

// Staged returns the entries with index changes, sorted by path.
func (s Status) Staged() []StatusEntry { return s.filter(StatusEntry.HasStaged) }

// Unstaged returns the entries with worktree changes (including untracked
// files) that are not in conflict, sorted by path.
func (s Status) Unstaged() []StatusEntry {
	return s.filter(func(f StatusEntry) bool { return !f.Conflict && f.HasUnstaged() })
}

// Conflicted returns the unmerged entries, sorted by path.
func (s Status) Conflicted() []StatusEntry {
	return s.filter(func(f StatusEntry) bool { return f.Conflict })
}

func (s Status) filter(keep func(StatusEntry) bool) []StatusEntry {
	var out []StatusEntry
	for _, f := range s.Files {
		if keep(f) {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b StatusEntry) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// Status reads the working tree state in one call: branch header, stash count
// and every changed, untracked or conflicted path with renames detected.
func (g *Git) Status(ctx context.Context) (Status, error) {
	out, err := g.Run(ctx, git.RunOpts{Background: true},
		"status", "--porcelain=v2", "-z", "--branch", "--show-stash",
		"--untracked-files=all", "--find-renames=50%")
	if err != nil {
		return Status{}, err
	}
	st, err := parseStatusV2(out)
	if err != nil {
		return Status{}, err
	}
	st.InProgress = g.readInProgress(ctx)
	return st, nil
}

// parseStatusV2 parses `git status --porcelain=v2 -z --branch --show-stash`
// output. Records are NUL-terminated. a rename/copy record is followed by one
// extra NUL-terminated field holding the original path.
func parseStatusV2(out string) (Status, error) {
	var st Status
	tokens := strings.Split(out, "\x00")
	for i := 0; i < len(tokens); i++ {
		rec := tokens[i]
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			parseHeader(&st, rec)
		case '1':
			f, err := parseOrdinary(rec)
			if err != nil {
				return Status{}, err
			}
			st.Files = append(st.Files, f)
		case '2':
			if i+1 >= len(tokens) || tokens[i+1] == "" {
				return Status{}, fmt.Errorf("status: rename record %q has no original path", rec)
			}
			f, err := parseRename(rec, tokens[i+1])
			if err != nil {
				return Status{}, err
			}
			i++
			st.Files = append(st.Files, f)
		case 'u':
			f, err := parseUnmerged(rec)
			if err != nil {
				return Status{}, err
			}
			st.Files = append(st.Files, f)
		case '?':
			st.Files = append(st.Files, StatusEntry{Path: rec[2:], Untracked: true, Index: '?', Worktree: '?'})
		case '!':
			st.Files = append(st.Files, StatusEntry{Path: rec[2:], Ignored: true, Index: '!', Worktree: '!'})
		default:
			return Status{}, fmt.Errorf("status: unrecognized record %q", rec)
		}
	}
	return st, nil
}

// parseHeader handles "# branch.*" and "# stash N" lines.
func parseHeader(st *Status, rec string) {
	fields := strings.Fields(rec)
	if len(fields) < 3 {
		return
	}
	switch fields[1] {
	case "branch.oid":
		if fields[2] == "(initial)" {
			st.Head.Unborn = true
		} else {
			st.Head.OID = fields[2]
		}
	case "branch.head":
		if fields[2] == "(detached)" {
			st.Head.Detached = true
		} else {
			st.Head.Name = fields[2]
		}
	case "branch.upstream":
		st.Head.Upstream = fields[2]
		// "branch.ab" follows when the upstream exists. parseStatusV2's caller
		// cannot know the order, so assume gone until the ab line clears it.
		st.Head.UpstreamGone = true
	case "branch.ab":
		if len(fields) >= 4 {
			st.Head.Ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
			st.Head.Behind, _ = strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
		}
		st.Head.UpstreamGone = false
	case "stash":
		st.StashCount, _ = strconv.Atoi(fields[2])
	}
}

// parseOrdinary handles "1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>".
func parseOrdinary(rec string) (StatusEntry, error) {
	fields := strings.SplitN(rec, " ", 9)
	if len(fields) < 9 {
		return StatusEntry{}, fmt.Errorf("status: malformed entry %q", rec)
	}
	f, err := xyStatus(fields[1], rec)
	if err != nil {
		return StatusEntry{}, err
	}
	f.Submodule = strings.HasPrefix(fields[2], "S")
	f.Path = fields[8]
	return f, nil
}

// parseRename handles "2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path>"
// followed by the original path in the next NUL field.
func parseRename(rec, origPath string) (StatusEntry, error) {
	fields := strings.SplitN(rec, " ", 10)
	if len(fields) < 10 {
		return StatusEntry{}, fmt.Errorf("status: malformed rename entry %q", rec)
	}
	f, err := xyStatus(fields[1], rec)
	if err != nil {
		return StatusEntry{}, err
	}
	f.Submodule = strings.HasPrefix(fields[2], "S")
	if len(fields[8]) > 1 {
		f.RenameScore, _ = strconv.Atoi(fields[8][1:])
	}
	f.Path = fields[9]
	f.OrigPath = origPath
	return f, nil
}

// parseUnmerged handles "u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>".
func parseUnmerged(rec string) (StatusEntry, error) {
	fields := strings.SplitN(rec, " ", 11)
	if len(fields) < 11 {
		return StatusEntry{}, fmt.Errorf("status: malformed unmerged entry %q", rec)
	}
	if len(fields[1]) != 2 {
		return StatusEntry{}, fmt.Errorf("status: malformed XY in %q", rec)
	}
	return StatusEntry{
		Path:       fields[10],
		Index:      ChangeCode(fields[1][0]),
		Worktree:   ChangeCode(fields[1][1]),
		Conflict:   true,
		ConflictXY: fields[1],
		Submodule:  strings.HasPrefix(fields[2], "S"),
	}, nil
}

func xyStatus(xy, rec string) (StatusEntry, error) {
	if len(xy) != 2 {
		return StatusEntry{}, fmt.Errorf("status: malformed XY in %q", rec)
	}
	return StatusEntry{Index: ChangeCode(xy[0]), Worktree: ChangeCode(xy[1])}, nil
}
