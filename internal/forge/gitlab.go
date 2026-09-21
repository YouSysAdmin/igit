package forge

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/yousysadmin/igit/internal/annot"
)

// GitLab reviews a merge request through the glab CLI. Comments are created
// as draft notes and published together, so the request gets one review and
// its author one notification, like a GitHub review.
type GitLab struct {
	dir     string
	run     Runner
	version gitlabVersion // read once by Resolve, zero when the probe failed
}

// NewGitLab returns a client for the clone at dir.
func NewGitLab(dir string) *GitLab { return &GitLab{dir: dir, run: execRunner} }

// NewGitLabWithRunner returns a client that executes commands through run.
func NewGitLabWithRunner(dir string, run Runner) *GitLab { return &GitLab{dir: dir, run: run} }

// Available reports whether the glab CLI can be found.
func (g *GitLab) Available() error {
	if _, err := exec.LookPath("glab"); err != nil {
		return errors.New("glab CLI not found, install it from https://gitlab.com/gitlab-org/cli and run glab auth login")
	}
	return nil
}

// gitlabVersion is the instance version glab reports, major.minor.
type gitlabVersion struct {
	major, minor int
}

func (v gitlabVersion) atLeast(major, minor int) bool {
	return v.major > major || (v.major == major && v.minor >= minor)
}

// parseGitLabVersion reads "16.7.0" or "19.5.0-pre" into a version.
func parseGitLabVersion(s string) (gitlabVersion, bool) {
	majorStr, rest, ok := strings.Cut(strings.TrimSpace(s), ".")
	if !ok {
		return gitlabVersion{}, false
	}
	minorStr, _, _ := strings.Cut(rest, ".")
	major, err1 := strconv.Atoi(majorStr)
	minor, err2 := strconv.Atoi(minorStr)
	if err1 != nil || err2 != nil {
		return gitlabVersion{}, false
	}
	return gitlabVersion{major: major, minor: minor}, true
}

// requestChangesSince is the first release whose GraphQL API has
// mergeRequestRequestChanges. Older instances only comment and approve.
var requestChangesSince = gitlabVersion{major: 17, minor: 0}

// readVersion probes the instance once. a failure leaves the version zero,
// which is treated as current.
func (g *GitLab) readVersion(ctx context.Context) {
	if g.version.major != 0 {
		return
	}
	out, err := g.run(ctx, g.dir, "glab", "", "api", "version")
	if err != nil {
		return
	}
	var v struct {
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		return
	}
	if parsed, ok := parseGitLabVersion(v.Version); ok {
		g.version = parsed
	}
}

// Kind names the service.
func (g *GitLab) Kind() Kind { return KindGitLab }

// Verdicts offers request changes only where the instance supports it.
func (g *GitLab) Verdicts() []Event {
	if g.version.major != 0 && !g.version.atLeast(requestChangesSince.major, requestChangesSince.minor) {
		return []Event{EventComment, EventApprove}
	}
	return slices.Clone(allVerdicts)
}

// IsNoRequest reports whether err is glab telling that the current branch has
// no open merge request.
func (g *GitLab) IsNoRequest(err error) bool {
	return err != nil && strings.Contains(err.Error(), "No open merge request available")
}

var mergeURLRe = regexp.MustCompile(`/-/merge_requests/(\d+)`)

// mergeRequestArg turns what the user typed into what glab mr view accepts: a
// URL becomes its iid, a "!12" reference loses the sigil, numbers and branch
// names pass through.
func mergeRequestArg(ref string) string {
	if m := mergeURLRe.FindStringSubmatch(ref); m != nil {
		return m[1]
	}
	return strings.TrimPrefix(ref, "!")
}

// projectPath reads the full project path from a "group/sub/proj!12" reference,
// falling back to the web URL.
func projectPath(reference, webURL string) string {
	if project, _, ok := strings.Cut(reference, "!"); ok && project != "" {
		return project
	}
	if u, err := url.Parse(webURL); err == nil {
		if project, _, ok := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/-/"); ok {
			return project
		}
	}
	return ""
}

// Resolve looks up a merge request through glab: ref is an iid, a "!iid"
// reference, a URL, a branch name or empty for the request of the current
// branch.
func (g *GitLab) Resolve(ctx context.Context, ref string) (PullRequest, error) {
	args := []string{"mr", "view"}
	if arg := mergeRequestArg(ref); arg != "" {
		args = append(args, arg)
	}
	args = append(args, "-F", "json")
	out, err := g.run(ctx, g.dir, "glab", "", args...)
	if err != nil {
		return PullRequest{}, fmt.Errorf("resolve merge request: %w", err)
	}
	var v struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		WebURL       string `json:"web_url"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		SHA          string `json:"sha"`
		DiffRefs     struct {
			BaseSHA  string `json:"base_sha"`
			HeadSHA  string `json:"head_sha"`
			StartSHA string `json:"start_sha"`
		} `json:"diff_refs"`
		References struct {
			Full string `json:"full"`
		} `json:"references"`
	}
	if uerr := json.Unmarshal([]byte(out), &v); uerr != nil {
		return PullRequest{}, fmt.Errorf("parse glab mr view output: %w", uerr)
	}
	head := v.DiffRefs.HeadSHA
	if head == "" {
		head = v.SHA
	}
	if head == "" || v.TargetBranch == "" {
		return PullRequest{}, fmt.Errorf("merge request !%d has no head commit or target branch", v.IID)
	}
	project := projectPath(v.References.Full, v.WebURL)
	if project == "" {
		return PullRequest{}, fmt.Errorf("merge request !%d names no project", v.IID)
	}
	g.readVersion(ctx)
	return PullRequest{
		Forge: KindGitLab, Project: project, Number: v.IID, URL: v.WebURL, Title: strings.TrimSpace(v.Title),
		BaseRef: v.TargetBranch, HeadRef: v.SourceBranch, HeadSHA: head,
		BaseSHA: v.DiffRefs.BaseSHA, StartSHA: v.DiffRefs.StartSHA,
	}, nil
}

// Prepare fetches the merge request head and the target branch from the remote
// that points at the project, then settles the merge base: the one the
// service computed when the clone has it, otherwise git works it out. Nothing
// is checked out.
func (g *GitLab) Prepare(ctx context.Context, pr *PullRequest) error {
	remote := remoteFor(ctx, g.run, g.dir, pr.Project)
	headRef := fmt.Sprintf("+refs/merge-requests/%d/head:refs/remotes/%s/mr/%d", pr.Number, remote, pr.Number)
	baseRef := fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", pr.BaseRef, remote, pr.BaseRef)
	if _, err := g.run(ctx, g.dir, "git", "", "fetch", "--no-tags", "--no-write-fetch-head", remote, headRef, baseRef); err != nil {
		return fmt.Errorf("fetch merge request !%d from %s: %w", pr.Number, remote, err)
	}
	pr.Remote = remote
	if pr.StartSHA == "" {
		out, err := g.run(ctx, g.dir, "git", "", "rev-parse", remote+"/"+pr.BaseRef)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", pr.BaseRef, err)
		}
		pr.StartSHA = strings.TrimSpace(out)
	}
	if pr.BaseSHA != "" {
		if _, err := g.run(ctx, g.dir, "git", "", "cat-file", "-e", pr.BaseSHA+"^{commit}"); err == nil {
			pr.MergeBase = pr.BaseSHA
			return nil
		}
	}
	out, err := g.run(ctx, g.dir, "git", "", "merge-base", remote+"/"+pr.BaseRef, pr.HeadSHA)
	if err != nil {
		return fmt.Errorf("merge base of %s and %s: %w", pr.BaseRef, pr.HeadSHA, err)
	}
	pr.MergeBase = strings.TrimSpace(out)
	if pr.MergeBase == "" {
		return fmt.Errorf("no merge base between %s and merge request !%d", pr.BaseRef, pr.Number)
	}
	pr.BaseSHA = pr.MergeBase
	return nil
}

// Plan anchors the annotations to the merge request diff.
func (g *GitLab) Plan(ctx context.Context, pr PullRequest, annots []annot.Annotation) (Plan, error) {
	plan, _, err := anchor(ctx, g.run, g.dir, pr, annots)
	return plan, err
}

// apiPath is the REST path of the merge request.
func apiPath(pr PullRequest) string {
	return fmt.Sprintf("projects/%s/merge_requests/%d", url.PathEscape(pr.Project), pr.Number)
}

// position is the diff anchor of a GitLab note.
type position struct {
	BaseSHA      string `json:"base_sha"`
	HeadSHA      string `json:"head_sha"`
	StartSHA     string `json:"start_sha"`
	PositionType string `json:"position_type"`
	NewPath      string `json:"new_path"`
	OldPath      string `json:"old_path"`
	NewLine      int    `json:"new_line,omitzero"`
	OldLine      int    `json:"old_line,omitzero"`
}

// draftNote is the POST draft_notes payload. Position is nil for a note on the
// request itself.
type draftNote struct {
	Note     string    `json:"note"`
	Position *position `json:"position,omitzero"`
}

// noteFor builds the draft note of one anchored comment. Removed lines sit on
// the old side, added lines on the new side and unchanged lines carry both
// numbers, as the API requires. A range is anchored on its first line and
// named in the text, line ranges are not sent.
func noteFor(pr PullRequest, c Comment, dl *diffLines) draftNote {
	line, body := c.Line, c.Body
	if c.StartLine > 0 {
		line = c.StartLine
		body = fmt.Sprintf("lines %d-%d: %s", c.StartLine, c.Line, c.Body)
	}
	p := &position{
		BaseSHA: pr.BaseSHA, HeadSHA: pr.HeadSHA, StartSHA: pr.StartSHA,
		PositionType: "text", NewPath: c.Path, OldPath: c.Path,
	}
	if c.Side == "LEFT" {
		p.OldLine = line
	} else {
		p.NewLine = line
		if dl != nil {
			if old, ok := dl.oldOfNew[line]; ok {
				p.OldLine = old
			}
		}
	}
	return draftNote{Note: body, Position: p}
}

// post sends a JSON body to a REST endpoint through glab. the responses carry
// nothing the session needs, so only the outcome is reported.
func (g *GitLab) post(ctx context.Context, endpoint string, body any) error {
	if body == nil {
		_, err := g.run(ctx, g.dir, "glab", "", "api", "-X", "POST", endpoint)
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	// glab sends --input bodies untyped and GitLab answers 415 to a nested
	// position without the JSON content type
	_, err = g.run(ctx, g.dir, "glab", string(payload), "api", "-X", "POST", endpoint, "--input", "-", "-H", "Content-Type: application/json")
	return err
}

// Submit posts the annotations as draft notes, publishes them together and
// applies the verdict: approve, or request changes through GraphQL.
func (g *GitLab) Submit(ctx context.Context, pr PullRequest, event Event, annots []annot.Annotation) (Submission, error) {
	plan, diffs, err := anchor(ctx, g.run, g.dir, pr, annots)
	if err != nil {
		return Submission{}, err
	}
	base := apiPath(pr)
	for _, c := range plan.Comments {
		if err := g.post(ctx, base+"/draft_notes", noteFor(pr, c, diffs[c.Path])); err != nil {
			return Submission{}, fmt.Errorf("post draft note on %s:%d: %w", c.Path, c.Line, err)
		}
	}
	if body := bodyFor(plan.Outside); body != "" {
		if err := g.post(ctx, base+"/draft_notes", draftNote{Note: body}); err != nil {
			return Submission{}, fmt.Errorf("post review note: %w", err)
		}
	}
	if len(plan.Comments) > 0 || len(plan.Outside) > 0 {
		if err := g.post(ctx, base+"/draft_notes/bulk_publish", nil); err != nil {
			return Submission{}, fmt.Errorf("publish review: %w", err)
		}
	}
	switch event {
	case EventApprove:
		if err := g.post(ctx, base+"/approve", map[string]string{"sha": pr.HeadSHA}); err != nil {
			return Submission{}, fmt.Errorf("approve: %w", err)
		}
	case EventRequestChanges:
		if err := g.requestChanges(ctx, pr); err != nil {
			return Submission{}, err
		}
	case EventComment:
	}
	return Submission{URL: pr.URL, Event: event, Comments: len(plan.Comments), InBody: len(plan.Outside)}, nil
}

// requestChanges flips the reviewer state through the GraphQL mutation. Only
// a reviewer can request changes, so a user who is not one yet is added to
// the reviewers first, as the web UI does, and the mutation runs again.
func (g *GitLab) requestChanges(ctx context.Context, pr PullRequest) error {
	err := g.mutate(ctx, "mergeRequestRequestChanges", fmt.Sprintf("{projectPath: %q, iid: %q}", pr.Project, strconv.Itoa(pr.Number)))
	if err == nil || !strings.Contains(err.Error(), "Reviewer not found") {
		return wrapVerdict("request changes", err)
	}
	if aerr := g.addSelfAsReviewer(ctx, pr); aerr != nil {
		return wrapVerdict("request changes", aerr)
	}
	return wrapVerdict("request changes", g.mutate(ctx, "mergeRequestRequestChanges", fmt.Sprintf("{projectPath: %q, iid: %q}", pr.Project, strconv.Itoa(pr.Number))))
}

// wrapVerdict names a failed verdict and says what state the request is in.
func wrapVerdict(verdict string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w (notes are published, the verdict is not)", verdict, err)
}

// addSelfAsReviewer appends the authenticated user to the reviewers.
func (g *GitLab) addSelfAsReviewer(ctx context.Context, pr PullRequest) error {
	out, err := g.run(ctx, g.dir, "glab", "", "api", "user")
	if err != nil {
		return fmt.Errorf("current user: %w", err)
	}
	var me struct {
		Username string `json:"username"`
	}
	if uerr := json.Unmarshal([]byte(out), &me); uerr != nil || me.Username == "" {
		return errors.New("current user: glab api user returned no username")
	}
	input := fmt.Sprintf("{projectPath: %q, iid: %q, reviewerUsernames: [%q], operationMode: APPEND}", pr.Project, strconv.Itoa(pr.Number), me.Username)
	if merr := g.mutate(ctx, "mergeRequestSetReviewers", input); merr != nil {
		return fmt.Errorf("add %s as reviewer: %w", me.Username, merr)
	}
	return nil
}

// mutate runs one GraphQL mutation and reports its errors, both the
// transport ones and the ones GitLab lists in the payload.
func (g *GitLab) mutate(ctx context.Context, name, input string) error {
	query := fmt.Sprintf("mutation { %s(input: %s) { errors } }", name, input)
	out, err := g.run(ctx, g.dir, "glab", "", "api", "graphql", "-f", "query="+query)
	if err != nil {
		return err
	}
	var resp struct {
		Data map[string]struct {
			Errors []string `json:"errors"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if uerr := json.Unmarshal([]byte(out), &resp); uerr != nil {
		return fmt.Errorf("parse %s response: %w", name, uerr)
	}
	if len(resp.Errors) > 0 {
		return errors.New(resp.Errors[0].Message)
	}
	if res, ok := resp.Data[name]; ok && len(res.Errors) > 0 {
		return errors.New(strings.Join(res.Errors, ", "))
	}
	return nil
}

// Comments returns the diff notes of the request that are still open: system
// notes, plain notes and resolved threads are left out. Every page is fetched.
func (g *GitLab) Comments(ctx context.Context, pr PullRequest) ([]LineComment, error) {
	out, err := g.run(ctx, g.dir, "glab", "", "api", "--paginate", apiPath(pr)+"/discussions")
	if err != nil {
		return nil, fmt.Errorf("list discussions: %w", err)
	}
	type note struct {
		Type     string `json:"type"`
		Body     string `json:"body"`
		System   bool   `json:"system"`
		Resolved bool   `json:"resolved"`
		Author   struct {
			Username string `json:"username"`
		} `json:"author"`
		Position *struct {
			NewPath string `json:"new_path"`
			OldPath string `json:"old_path"`
			NewLine *int   `json:"new_line"`
			OldLine *int   `json:"old_line"`
		} `json:"position"`
	}
	type discussion struct {
		Notes []note `json:"notes"`
	}
	// glab prints every page as its own JSON document
	dec := jsontext.NewDecoder(strings.NewReader(out))
	var comments []LineComment
	for {
		var page []discussion
		if derr := json.UnmarshalDecode(dec, &page); derr != nil {
			if errors.Is(derr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse discussions: %w", derr)
		}
		for _, d := range page {
			for _, n := range d.Notes {
				if n.Type != "DiffNote" || n.System || n.Resolved || n.Position == nil {
					continue
				}
				lc := LineComment{Author: n.Author.Username, Body: normalizeBody(n.Body), Side: "RIGHT", Path: n.Position.NewPath}
				switch {
				case n.Position.NewLine != nil && *n.Position.NewLine > 0:
					lc.Line = *n.Position.NewLine
				case n.Position.OldLine != nil && *n.Position.OldLine > 0:
					lc.Line, lc.Side, lc.Path = *n.Position.OldLine, "LEFT", n.Position.OldPath
				default:
					continue
				}
				if lc.Path == "" {
					continue
				}
				comments = append(comments, lc)
			}
		}
	}
	return comments, nil
}

// ListOpen returns the project's open merge requests, newest first.
func (g *GitLab) ListOpen(ctx context.Context) ([]Summary, error) {
	out, err := g.run(ctx, g.dir, "glab", "", "mr", "list", "--per-page", "100", "-F", "json")
	if err != nil {
		return nil, fmt.Errorf("list merge requests: %w", err)
	}
	var v []struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		SourceBranch string `json:"source_branch"`
		Draft        bool   `json:"draft"`
		Author       struct {
			Username string `json:"username"`
		} `json:"author"`
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("parse merge request list: %w", err)
	}
	list := make([]Summary, 0, len(v))
	for _, m := range v {
		list = append(list, Summary{Number: m.IID, Title: strings.TrimSpace(m.Title), Author: m.Author.Username, Branch: m.SourceBranch, Draft: m.Draft})
	}
	return list, nil
}

// Since resolves what an incremental review starts from. The service part is
// not wired yet: an empty rev would come from merge_requests/:iid/versions, so
// for now only an explicit revision works.
func (g *GitLab) Since(ctx context.Context, pr PullRequest, rev string) (Baseline, error) {
	if rev == "" {
		return Baseline{}, nil
	}
	return baselineFor(ctx, g.run, g.dir, pr, rev)
}
