package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
)

// PREvent is the verdict a pull-request review is submitted with.
type PREvent string

const (
	PRComment        PREvent = "COMMENT"
	PRApprove        PREvent = "APPROVE"
	PRRequestChanges PREvent = "REQUEST_CHANGES"
)

// PRPlan says how the session's annotations would be posted: anchored to diff
// lines, or folded into the review body because their line is outside the
// pull request's diff.
type PRPlan struct {
	Comments int
	InBody   int
}

// PRSubmission reports a posted review.
type PRSubmission struct {
	URL      string
	Event    PREvent
	Comments int
	InBody   int
}

// PRReviewer posts the session's annotations to the pull request under
// review. The composition root implements it over the forge package. nil
// means the session is not a pull-request review and quitting never asks.
type PRReviewer interface {
	Describe() string // e.g. "PR #12: title"
	Plan(ctx context.Context, annots []annot.Annotation) (PRPlan, error)
	Submit(ctx context.Context, event PREvent, annots []annot.Annotation) (PRSubmission, error)
	// Verdicts lists the events the service accepts, in menu order. The quit
	// menu offers only these.
	Verdicts() []PREvent
}

// prVerdictItems maps every verdict to its quit-menu entry, in menu order.
var prVerdictItems = []struct {
	event PREvent
	item  overlay.MenuItem
}{
	{PRComment, overlay.MenuItem{Key: 'c', Label: "Comment", ID: prMenuComment}},
	{PRApprove, overlay.MenuItem{Key: 'a', Label: "Approve", ID: prMenuApprove}},
	{PRRequestChanges, overlay.MenuItem{Key: 'r', Label: "Request changes", ID: prMenuRequestChanges}},
}

// prState drives the submit-on-quit step of a pull-request review.
type prState struct {
	reviewer   PRReviewer
	submitting bool          // a Submit is in flight, keys are ignored until it reports
	skip       bool          // the user chose to quit without posting
	result     *PRSubmission // set once a review was posted, read by the composition root
	hint       string        // transient status-bar message
}

// prSubmittedMsg carries the outcome of an asynchronous Submit.
type prSubmittedMsg struct {
	sub PRSubmission
	err error
}

// menu item ids of the quit menu
const (
	prMenuComment        = "pr:comment"
	prMenuApprove        = "pr:approve"
	prMenuRequestChanges = "pr:request-changes"
	prMenuSkip           = "pr:skip"
)

// PRSubmission returns the review posted during the session, or nil.
func (m Model) PRSubmission() *PRSubmission { return m.pr.result }

// handleQuitWithPR intercepts quit in a pull-request session that has
// annotations: instead of leaving, it opens the submit menu. handled is false
// when there is nothing to post or the user already declined.
func (m Model) handleQuitWithPR() (Model, bool) {
	if m.pr.reviewer == nil || m.pr.skip {
		return m, false
	}
	n := m.store.Count()
	// the title lives in the popup border and is dropped when it does not fit,
	// so keep it short: the request name is cut, the counts stay
	name := sidepane.TruncateRight(m.pr.reviewer.Describe(), 24)
	title := name + ": nothing to post"
	if n > 0 {
		title = fmt.Sprintf("%s: post %d %s", name, n, pluralAnnotations(n))
		if plan, err := m.pr.reviewer.Plan(context.Background(), m.annotationsList()); err == nil && plan.InBody > 0 {
			title += fmt.Sprintf(" (%d in the body)", plan.InBody)
		}
	}
	offered := m.pr.reviewer.Verdicts()
	items := make([]overlay.MenuItem, 0, len(prVerdictItems)+1)
	for _, v := range prVerdictItems {
		if !slices.Contains(offered, v.event) {
			continue
		}
		// a review that found nothing still carries a verdict, but a comment
		// with neither comments nor a body is one both services reject
		if n == 0 && v.event == PRComment {
			continue
		}
		items = append(items, v.item)
	}
	skip := "Quit without posting"
	if n == 0 {
		skip = "Quit without a verdict"
	}
	items = append(items, overlay.MenuItem{Key: 's', Label: skip, ID: prMenuSkip})
	if len(items) == 1 {
		return m, false // only the way out is left, so leaving is what q means
	}
	m.overlay.OpenMenu(overlay.MenuSpec{Title: title, Items: items})
	return m, true
}

// handlePRMenuChoice acts on the quit menu: skip leaves immediately, a verdict
// posts the review off the UI goroutine and quits once it has landed.
func (m Model) handlePRMenuChoice(id string) (tea.Model, tea.Cmd) {
	var event PREvent
	switch id {
	case prMenuSkip:
		m.pr.skip = true
		return m, tea.Quit
	case prMenuComment:
		event = PRComment
	case prMenuApprove:
		event = PRApprove
	case prMenuRequestChanges:
		event = PRRequestChanges
	default:
		return m, nil
	}
	m.pr.submitting = true
	m.pr.hint = "posting review to " + m.pr.reviewer.Describe() + "…"
	reviewer, annots := m.pr.reviewer, m.annotationsList()
	return m, func() tea.Msg {
		sub, err := reviewer.Submit(context.Background(), event, annots)
		return prSubmittedMsg{sub: sub, err: err}
	}
}

// handlePRSubmitted quits after a successful post. a failure opens the error
// popup and leaves the session open so the user can retry or quit without posting.
func (m Model) handlePRSubmitted(msg prSubmittedMsg) (tea.Model, tea.Cmd) {
	m.pr.submitting = false
	m.pr.hint = ""
	if msg.err != nil {
		m.overlay.OpenError(overlay.ErrorSpec{
			Title:   "review not posted",
			Summary: firstLineOf(msg.err.Error()),
			Detail:  msg.err.Error() + "\n\nq again retries, s in the menu quits and keeps the local output.",
		})
		return m, nil
	}
	m.pr.result = new(msg.sub)
	return m, tea.Quit
}

// isPRMenuChoice reports whether a menu id belongs to the quit menu.
func isPRMenuChoice(id string) bool { return strings.HasPrefix(id, "pr:") }

// annotationsList flattens the store in file order.
func (m Model) annotationsList() []annot.Annotation {
	out := make([]annot.Annotation, 0, m.store.Count())
	for _, f := range m.store.Files() {
		out = append(out, m.store.Get(f)...)
	}
	return out
}
