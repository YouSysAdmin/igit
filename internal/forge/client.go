// Package forge connects a review session to the code-hosting service that
// owns the request: resolving it, fetching its commits into the local clone
// and posting the session's annotations as a review. GitHub goes through the
// gh CLI and GitLab through glab, both supply authentication. git handles
// everything that touches the repository.
package forge

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/patch"
)

// Kind names the hosting service behind a request.
type Kind string

const (
	KindGitHub Kind = "github"
	KindGitLab Kind = "gitlab"
)

// Client is what a hosting service offers a review session: resolve the
// request, fetch its commits, anchor annotations to its diff and post them.
// The composition root picks one with Open, the TUI sees it through an adapter.
type Client interface {
	Available() error
	Resolve(ctx context.Context, ref string) (PullRequest, error)
	Prepare(ctx context.Context, pr *PullRequest) error
	Plan(ctx context.Context, pr PullRequest, annots []annot.Annotation) (Plan, error)
	Submit(ctx context.Context, pr PullRequest, event Event, annots []annot.Annotation) (Submission, error)
	Comments(ctx context.Context, pr PullRequest) ([]LineComment, error)
	ListOpen(ctx context.Context) ([]Summary, error)
	// Kind names the service.
	Kind() Kind
	// Verdicts lists the events the service accepts, the quit menu offers them.
	Verdicts() []Event
	// IsNoRequest reports whether err says the checked-out branch has no
	// request, the cue to offer the open ones instead.
	IsNoRequest(err error) bool
}

// PullRequest identifies a pull or merge request and the commits its review
// compares: the merge base of the head with the base branch, and the head.
type PullRequest struct {
	Forge     Kind
	Owner     string // GitHub owner
	Repo      string // GitHub repository
	Project   string // GitLab project path, groups included
	Number    int
	URL       string
	Title     string
	BaseRef   string // base branch name
	HeadRef   string // head branch name
	HeadSHA   string
	BaseSHA   string // GitLab diff_refs.base_sha, the merge base the service computed
	StartSHA  string // GitLab diff_refs.start_sha, the base branch commit
	Remote    string // local remote the request was fetched from, set by Prepare
	MergeBase string // merge base of HeadSHA and the base branch, set by Prepare
}

// Ref is the two-ref range the review renders: merge base .. head.
func (pr PullRequest) Ref() string { return pr.MergeBase + ".." + pr.HeadSHA }

// Name is the short reference of the request: "PR #12" or "MR !12".
func (pr PullRequest) Name() string {
	if pr.Forge == KindGitLab {
		return fmt.Sprintf("MR !%d", pr.Number)
	}
	return fmt.Sprintf("PR #%d", pr.Number)
}

// Label names the request for headers: "PR #12: title".
func (pr PullRequest) Label() string {
	if pr.Title == "" {
		return pr.Name()
	}
	return pr.Name() + ": " + pr.Title
}

// Noun is how the service calls a request: "pull request" or "merge request".
func (pr PullRequest) Noun() string { return KindNoun(pr.Forge) }

// KindNoun is the request noun of a service.
func KindNoun(k Kind) string {
	if k == KindGitLab {
		return "merge request"
	}
	return "pull request"
}

// ScopeKind is the history scope prefix: "pr" or "mr".
func (pr PullRequest) ScopeKind() string {
	if pr.Forge == KindGitLab {
		return "mr"
	}
	return "pr"
}

// Event is a review verdict. GitHub takes all three, GitLab maps them to
// publishing the notes, approving and requesting changes.
type Event string

const (
	EventComment        Event = "COMMENT"
	EventApprove        Event = "APPROVE"
	EventRequestChanges Event = "REQUEST_CHANGES"
)

// Comment is one anchored review comment in the GitHub API shape: a line (or
// line range) on one side of the diff. GitLab derives its position from it.
type Comment struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Line      int    `json:"line,omitzero"`
	Side      string `json:"side,omitempty"`
	StartLine int    `json:"start_line,omitzero"`
	StartSide string `json:"start_side,omitempty"`
}

// Plan is what a set of annotations becomes: comments the service can anchor
// to a diff line, and annotations it cannot anchor (whole-file notes, lines
// outside the request's diff), which go into the review body as text because
// the APIs reject them as comments.
type Plan struct {
	Comments []Comment
	Outside  []annot.Annotation
}

// Submission reports a posted review.
type Submission struct {
	URL      string
	Event    Event
	Comments int // anchored comments
	InBody   int // annotations folded into the review body
}

// Runner executes an external command in dir with stdin and returns its
// stdout. The default runs the process. tests inject a fake.
type Runner func(ctx context.Context, dir, name, stdin string, args ...string) (string, error)

// CommandError is a failed gh, glab or git invocation with its stderr.
type CommandError struct {
	Name   string
	Args   []string
	Stderr string
	Err    error
}

func (e *CommandError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return e.Name + " " + strings.Join(e.Args, " ") + ": " + firstLine(msg)
}

func (e *CommandError) Unwrap() error { return e.Err }

// Detail returns the complete stderr for popups.
func (e *CommandError) Detail() string { return strings.TrimSpace(e.Stderr) }

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func execRunner(ctx context.Context, dir, name, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // args are built from parsed pull-request data, never from free text
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", &CommandError{Name: name, Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// remoteFor picks the remote whose URL names the project path (owner/repo on
// GitHub, the full group path on GitLab), falling back to origin.
func remoteFor(ctx context.Context, run Runner, dir, project string) string {
	out, err := run(ctx, dir, "git", "", "remote", "-v")
	if err != nil {
		return "origin"
	}
	want := strings.ToLower(project)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (len(fields) == 3 && fields[2] != "(fetch)") {
			continue
		}
		url := strings.ToLower(strings.TrimSuffix(fields[1], ".git"))
		if strings.HasSuffix(url, "/"+want) || strings.HasSuffix(url, ":"+want) {
			return fields[0]
		}
	}
	return "origin"
}

// bodyFor renders the annotations that cannot be anchored as a list in the
// review body, keeping their file (and line) so they stay actionable.
func bodyFor(outside []annot.Annotation) string {
	if len(outside) == 0 {
		return ""
	}
	sorted := slices.Clone(outside)
	slices.SortStableFunc(sorted, func(a, b annot.Annotation) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	var b strings.Builder
	b.WriteString("Notes that do not map to a diff line:\n")
	for _, a := range sorted {
		loc := fmt.Sprintf("%s:%d", a.File, a.Line)
		switch {
		case a.Line == 0:
			loc = a.File
		case a.EndLine > a.Line:
			loc = fmt.Sprintf("%s:%d-%d", a.File, a.Line, a.EndLine)
		}
		fmt.Fprintf(&b, "\n- `%s`: %s", loc, strings.TrimSpace(a.Comment))
	}
	return b.String()
}

// diffLines is the set of line numbers, per side, that a file's request diff
// shows. oldOfNew pairs a context line's new number with its old one, GitLab
// wants both for unchanged lines.
type diffLines struct {
	oldLines map[int]bool
	newLines map[int]bool
	oldOfNew map[int]int
}

// anchor turns annotations into a Plan: every file's diff is read once and
// each annotation is placed on it or reported in Outside. The per-file diffs
// come back too, for services that need more than a line and a side.
func anchor(ctx context.Context, run Runner, dir string, pr PullRequest, annots []annot.Annotation) (Plan, map[string]*diffLines, error) {
	var plan Plan
	diffs := map[string]*diffLines{}
	for _, a := range annots {
		if a.Line == 0 {
			// the review endpoints anchor comments to lines only. a whole-file
			// note goes into the body with its path
			plan.Outside = append(plan.Outside, a)
			continue
		}
		dl, ok := diffs[a.File]
		if !ok {
			var err error
			if dl, err = fileDiffLines(ctx, run, dir, pr, a.File); err != nil {
				return Plan{}, nil, err
			}
			diffs[a.File] = dl
		}
		c, ok := dl.comment(a)
		if !ok {
			plan.Outside = append(plan.Outside, a)
			continue
		}
		plan.Comments = append(plan.Comments, c)
	}
	return plan, diffs, nil
}

func fileDiffLines(ctx context.Context, run Runner, dir string, pr PullRequest, path string) (*diffLines, error) {
	raw, err := run(ctx, dir, "git", "", "diff", "--no-color", "--no-ext-diff", "-U3", pr.MergeBase, pr.HeadSHA, "--", path)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", path, err)
	}
	dl := &diffLines{oldLines: map[int]bool{}, newLines: map[int]bool{}, oldOfNew: map[int]int{}}
	if strings.TrimSpace(raw) == "" {
		return dl, nil
	}
	p, err := patch.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse diff of %s: %w", path, err)
	}
	for _, vl := range p.ViewLines() {
		switch vl.Kind { //nolint:exhaustive // header and hunk-header rows carry no line numbers
		case patch.KindContext:
			dl.oldLines[vl.OldNum] = true
			dl.newLines[vl.NewNum] = true
			dl.oldOfNew[vl.NewNum] = vl.OldNum
		case patch.KindAddition:
			dl.newLines[vl.NewNum] = true
		case patch.KindDeletion:
			dl.oldLines[vl.OldNum] = true
		}
	}
	return dl, nil
}

// comment anchors one annotation: removed lines sit on the old side, added
// and context lines on the new side. A range whose end is outside the diff
// collapses to its first line. a first line outside the diff cannot be posted.
func (dl *diffLines) comment(a annot.Annotation) (Comment, bool) {
	side, present := "RIGHT", dl.newLines
	if a.Type == "-" {
		side, present = "LEFT", dl.oldLines
	}
	if !present[a.Line] {
		return Comment{}, false
	}
	c := Comment{Path: a.File, Body: a.Comment, Line: a.Line, Side: side}
	if a.EndLine > a.Line && present[a.EndLine] {
		c.StartLine, c.StartSide, c.Line = a.Line, side, a.EndLine
	}
	return c, true
}

// LineComment is an existing review comment anchored to a diff line.
type LineComment struct {
	Path      string
	Line      int    // line on Side, the end of the range for multi-line comments
	StartLine int    // 0 for single-line comments
	Side      string // LEFT or RIGHT
	Author    string
	Body      string
}

// Summary is one entry of the open pull request list.
type Summary struct {
	Number int
	Title  string
	Author string
	Branch string
	Draft  bool
}

// IsNoPullRequest reports whether err is gh telling that the current branch
// has no pull request.
func IsNoPullRequest(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no pull requests found")
}

// allVerdicts is the full set of events, in menu order.
var allVerdicts = []Event{EventComment, EventApprove, EventRequestChanges}
