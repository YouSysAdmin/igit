// Package stageplan holds the deferred staging plan built during a review:
// files and change ranges the user marked "for the commit". The plan is pure
// data. gitops applies it to the index when the user switches to commit mode.
package stageplan

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"

	"github.com/yousysadmin/igit/internal/git"
)

// Range is one marked change block in working-tree coordinates: removed lines
// OldStart..OldEnd of the HEAD/index version, added lines NewStart..NewEnd of
// the worktree version (a bound is 0 when that side has no lines). Hash pins
// the exact content so a block edited after marking is detected.
type Range struct {
	OldStart, OldEnd int
	NewStart, NewEnd int
	Hash             string
}

// Covers reports whether a diff line with the given numbers is inside the range.
func (r Range) Covers(oldNum, newNum int) bool {
	if newNum > 0 && r.NewStart > 0 && newNum >= r.NewStart && newNum <= r.NewEnd {
		return true
	}
	return oldNum > 0 && r.OldStart > 0 && oldNum >= r.OldStart && oldNum <= r.OldEnd
}

// NewRange builds the range covering the added/removed lines in block. Context
// and divider lines are ignored.
func NewRange(block []git.DiffLine) Range {
	var r Range
	changes := make([]git.DiffLine, 0, len(block))
	for _, dl := range block {
		switch dl.ChangeType {
		case git.ChangeAdd:
			if r.NewStart == 0 || dl.NewNum < r.NewStart {
				r.NewStart = dl.NewNum
			}
			r.NewEnd = max(r.NewEnd, dl.NewNum)
		case git.ChangeRemove:
			if r.OldStart == 0 || dl.OldNum < r.OldStart {
				r.OldStart = dl.OldNum
			}
			r.OldEnd = max(r.OldEnd, dl.OldNum)
		case git.ChangeContext, git.ChangeDivider:
			continue
		}
		changes = append(changes, dl)
	}
	r.Hash = HashLines(changes)
	return r
}

// HashLines fingerprints the changed lines' kinds and contents, in order, so a
// range can be re-identified in a freshly generated diff.
func HashLines(changes []git.DiffLine) string {
	h := sha256.New()
	for _, dl := range changes {
		h.Write([]byte(string(dl.ChangeType)))
		h.Write([]byte(dl.Content))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// FileMarks is what the plan holds for one path: the whole file, or ranges.
type FileMarks struct {
	Whole  bool
	Ranges []Range
}

// Plan is the set of marks, keyed by path. Version increments on every change
// so renderers can cache against it.
type Plan struct {
	files   map[string]*FileMarks
	version uint64
}

// New returns an empty plan.
func New() *Plan { return &Plan{files: map[string]*FileMarks{}} }

// Version returns a counter that changes with every mutation.
func (p *Plan) Version() uint64 { return p.version }

// Empty reports whether nothing is marked.
func (p *Plan) Empty() bool { return len(p.files) == 0 }

// Count returns the number of marked files.
func (p *Plan) Count() int { return len(p.files) }

// Files returns the marked paths in sorted order.
func (p *Plan) Files() []string {
	return slices.Sorted(maps.Keys(p.files))
}

// Marks returns the marks for path. ok is false when the path is not in the plan.
func (p *Plan) Marks(path string) (FileMarks, bool) {
	m, ok := p.files[path]
	if !ok {
		return FileMarks{}, false
	}
	return FileMarks{Whole: m.Whole, Ranges: slices.Clone(m.Ranges)}, true
}

// Has reports whether path carries any mark.
func (p *Plan) Has(path string) bool { _, ok := p.files[path]; return ok }

// IsWhole reports whether the whole file is marked.
func (p *Plan) IsWhole(path string) bool {
	m, ok := p.files[path]
	return ok && m.Whole
}

// ToggleFile marks the whole file, or removes every mark on it when it was
// already whole-marked. Returns the new state.
func (p *Plan) ToggleFile(path string) bool {
	if p.IsWhole(path) {
		p.Remove(path)
		return false
	}
	p.files[path] = &FileMarks{Whole: true}
	p.version++
	return true
}

// ToggleRange adds r to path, or removes it when an identical range is
// present. A whole-file mark is replaced by the single range. Returns whether
// r is marked afterwards.
func (p *Plan) ToggleRange(path string, r Range) bool {
	m, ok := p.files[path]
	if ok && !m.Whole {
		if i := slices.Index(m.Ranges, r); i >= 0 {
			m.Ranges = slices.Delete(m.Ranges, i, i+1)
			if len(m.Ranges) == 0 {
				delete(p.files, path)
			}
			p.version++
			return false
		}
	}
	if !ok || m.Whole {
		m = &FileMarks{}
		p.files[path] = m
	}
	m.Ranges = append(m.Ranges, r)
	slices.SortFunc(m.Ranges, func(a, b Range) int {
		return cmp.Compare(max(a.NewStart, a.OldStart), max(b.NewStart, b.OldStart))
	})
	p.version++
	return true
}

// Covers reports whether the diff line (oldNum, newNum) of path is marked.
func (p *Plan) Covers(path string, oldNum, newNum int) bool {
	m, ok := p.files[path]
	if !ok {
		return false
	}
	if m.Whole {
		return true
	}
	for _, r := range m.Ranges {
		if r.Covers(oldNum, newNum) {
			return true
		}
	}
	return false
}

// Remove drops every mark on path.
func (p *Plan) Remove(path string) {
	if _, ok := p.files[path]; ok {
		delete(p.files, path)
		p.version++
	}
}

// Result is what applying a plan produced.
type Result struct {
	Staged  []string
	Skipped []Skipped
}

// Skipped is a file the plan could not stage and why.
type Skipped struct {
	Path   string
	Reason string
}
