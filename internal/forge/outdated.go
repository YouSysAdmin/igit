package forge

import (
	"context"
	"fmt"
	"strings"

	"github.com/yousysadmin/igit/internal/patch"
)

// maxLineMaps caps how many file diffs one session reads to place outdated
// comments. Comments past it keep their original line and fall back to a file
// note, which keeps startup bounded on a long-lived request.
const maxLineMaps = 64

// lineMap maps a file's line numbers at one commit onto its numbers at
// another. It is built from a full-context diff, where every surviving line
// appears as a context row carrying both numbers. An unchanged file produces
// an empty diff and maps every line onto itself.
type lineMap struct {
	newOf     map[int]int
	unchanged bool
}

// forward maps an old line number onto the new file. ok is false when the line
// did not survive.
func (l *lineMap) forward(old int) (int, bool) {
	if l.unchanged {
		return old, true
	}
	n, ok := l.newOf[old]
	return n, ok
}

// reanchorKey identifies the one diff that places every comment written
// against the same commit and path.
type reanchorKey struct {
	sha  string
	path string
}

// reanchor places the request's outdated comments on the current head: the
// line each was written against is mapped forward through git's own diff of
// the review commit against the head. A line that survived keeps its place, a
// line that did not becomes a file note, and so does every comment whose
// commit or diff cannot be read. Comments are never dropped.
func reanchor(ctx context.Context, run Runner, dir string, pr PullRequest, comments []LineComment) []LineComment {
	r := &reanchorer{run: run, dir: dir, pr: pr, maps: map[reanchorKey]*lineMap{}, local: map[string]bool{}}
	out := make([]LineComment, 0, len(comments))
	for _, c := range comments {
		out = append(out, r.place(ctx, c))
	}
	return out
}

// reanchorer carries the per-session memos so N comments on one file at one
// review commit cost one diff.
type reanchorer struct {
	run   Runner
	dir   string
	pr    PullRequest
	maps  map[reanchorKey]*lineMap
	local map[string]bool // commit availability, by sha
	built int             // diffs read so far
}

// place resolves one comment. A comment the service still anchors is returned
// untouched.
func (r *reanchorer) place(ctx context.Context, c LineComment) LineComment {
	if !c.Outdated || c.OrigLine == 0 || c.OrigSHA == "" {
		return c
	}
	// original_line on the left side numbers the base as it stood at review
	// time, which a diff of the head cannot map. Keep it and let the file-level
	// fallback catch it when the base moved.
	if c.Side == "LEFT" {
		c.Line = c.OrigLine
		return c
	}
	lm, ok := r.lineMapFor(ctx, c.OrigSHA, c.Path)
	if !ok {
		c.Line = 0
		return c
	}
	if n, survived := lm.forward(c.OrigLine); survived {
		c.Line = n
		return c
	}
	c.Line = 0
	return c
}

// lineMapFor reads the diff that maps path from sha onto the request head,
// memoized per commit and path.
func (r *reanchorer) lineMapFor(ctx context.Context, sha, path string) (*lineMap, bool) {
	key := reanchorKey{sha: sha, path: path}
	if lm, done := r.maps[key]; done {
		return lm, lm != nil
	}
	if !r.available(ctx, sha) || r.built >= maxLineMaps {
		r.maps[key] = nil
		return nil, false
	}
	r.built++
	// -M so a file renamed since the review still diffs, full context so every
	// surviving line is a context row with both numbers and no hunk arithmetic
	// is needed. One path per call: patch.Parse reads a single file.
	raw, err := r.run(ctx, r.dir, "git", "", "diff", "--no-color", "--no-ext-diff", "-M", "-U1000000", sha, r.pr.HeadSHA, "--", path)
	if err != nil {
		r.maps[key] = nil
		return nil, false
	}
	lm, err := parseLineMap(raw)
	if err != nil {
		r.maps[key] = nil
		return nil, false
	}
	r.maps[key] = lm
	return lm, true
}

// available reports whether the clone can read the commit, fetching it once by
// object name when it cannot. A force push leaves review commits unreachable
// from any ref, and a server that refuses the fetch simply leaves them missing.
func (r *reanchorer) available(ctx context.Context, sha string) bool {
	if ok, done := r.local[sha]; done {
		return ok
	}
	if r.hasCommit(ctx, sha) {
		r.local[sha] = true
		return true
	}
	if r.pr.Remote != "" && isFullHash(sha) {
		_, _ = r.run(ctx, r.dir, "git", "", "fetch", "--no-tags", "--no-write-fetch-head", r.pr.Remote, sha)
	}
	ok := r.hasCommit(ctx, sha)
	r.local[sha] = ok
	return ok
}

func (r *reanchorer) hasCommit(ctx context.Context, sha string) bool {
	_, err := r.run(ctx, r.dir, "git", "", "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// parseLineMap reads a full-context diff of one file and pairs every surviving
// old line with its new number. An empty diff is the identity: the file did not
// change between the two commits, so nothing needs mapping.
func parseLineMap(raw string) (*lineMap, error) {
	if strings.TrimSpace(raw) == "" {
		return &lineMap{unchanged: true}, nil
	}
	p, err := patch.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse diff: %w", err)
	}
	lm := &lineMap{newOf: map[int]int{}}
	for _, vl := range p.ViewLines() {
		if vl.Kind == patch.KindContext && vl.OldNum > 0 && vl.NewNum > 0 {
			lm.newOf[vl.OldNum] = vl.NewNum
		}
	}
	return lm, nil
}

// isFullHash reports whether sha is a complete object name, the only form a
// server serves by object name.
func isFullHash(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
