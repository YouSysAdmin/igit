package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// prReviewerStub records Submit calls and answers from its fields.
type prReviewerStub struct {
	plan      PRPlan
	sub       PRSubmission
	err       error
	verdicts  []PREvent // nil offers all three
	submitted []PREvent
	annots    []annot.Annotation
}

func (s *prReviewerStub) Describe() string { return "PR #12: fix parser" }
func (s *prReviewerStub) Verdicts() []PREvent {
	if s.verdicts == nil {
		return []PREvent{PRComment, PRApprove, PRRequestChanges}
	}
	return s.verdicts
}
func (s *prReviewerStub) Plan(context.Context, []annot.Annotation) (PRPlan, error) {
	return s.plan, nil
}
func (s *prReviewerStub) Submit(_ context.Context, event PREvent, annots []annot.Annotation) (PRSubmission, error) {
	s.submitted = append(s.submitted, event)
	s.annots = annots
	if s.err != nil {
		return PRSubmission{}, s.err
	}
	s.sub.Event = event
	return s.sub, nil
}

func newPRModel(t *testing.T, stub *prReviewerStub, annots ...annot.Annotation) Model {
	t.Helper()
	store := annot.NewStore()
	for _, a := range annots {
		store.Add(a)
	}
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{PRReview: stub})
	m.filesLoaded = true // render the layout instead of the loading placeholder
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model)
}

func quitRequested(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestModel_PRQuitOpensMenuAndSubmits(t *testing.T) {
	stub := &prReviewerStub{plan: PRPlan{Comments: 1, InBody: 1}, sub: PRSubmission{URL: "https://github.com/a/b/pull/12#r1", Comments: 1, InBody: 1}}
	m := newPRModel(t, stub,
		annot.Annotation{File: "b.go", Line: 3, Type: "+", Comment: "second"},
		annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "first"},
	)
	next, cmd := m.Update(keyRunes("q"))
	m = next.(Model)
	assert.Nil(t, cmd, "quit is intercepted")
	require.True(t, m.overlay.Active())
	assert.Equal(t, overlay.KindMenu, m.overlay.Kind())
	assert.True(t, m.inputBusy())
	view := m.View()
	assert.Contains(t, view, "PR #12: fix parser: post 2 annotations (1 in the body)")
	assert.Contains(t, view, "Approve")

	next, cmd = m.Update(keyRunes("a"))
	m = next.(Model)
	require.NotNil(t, cmd)
	assert.False(t, m.overlay.Active())
	assert.True(t, m.pr.submitting)
	assert.True(t, m.inputBusy())
	assert.Contains(t, m.View(), "posting review to PR #12")
	// keys are swallowed while the post is in flight
	next, _ = m.Update(keyRunes("j"))
	m = next.(Model)
	assert.Equal(t, 0, m.nav.diffCursor)

	msg := cmd()
	require.IsType(t, prSubmittedMsg{}, msg)
	assert.Equal(t, []PREvent{PRApprove}, stub.submitted)
	assert.Equal(t, []string{"a.go", "b.go"}, []string{stub.annots[0].File, stub.annots[1].File}, "annotations are handed over in file order")

	next, cmd = m.Update(msg)
	m = next.(Model)
	assert.True(t, quitRequested(cmd))
	require.NotNil(t, m.PRSubmission())
	assert.Equal(t, PRApprove, m.PRSubmission().Event)
	assert.Equal(t, "https://github.com/a/b/pull/12#r1", m.PRSubmission().URL)
	assert.False(t, m.Discarded())
}

func TestModel_PRQuitMenuOffersOnlyTheReviewerVerdicts(t *testing.T) {
	stub := &prReviewerStub{verdicts: []PREvent{PRComment, PRApprove}}
	m := newPRModel(t, stub, annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "first"})
	next, _ := m.Update(keyRunes("q"))
	m = next.(Model)
	require.Equal(t, overlay.KindMenu, m.overlay.Kind())
	view := m.View()
	assert.Contains(t, view, "Comment")
	assert.Contains(t, view, "Approve")
	assert.NotContains(t, view, "Request changes", "a verdict the service lacks is not offered")
	assert.Contains(t, view, "Quit without posting")

	// the key of the missing item does nothing
	next, cmd := m.Update(keyRunes("r"))
	m = next.(Model)
	assert.Nil(t, cmd)
	assert.True(t, m.overlay.Active(), "menu stays open")
	assert.Empty(t, stub.submitted)
}

func TestModel_PRQuitMenuChoices(t *testing.T) {
	for key, want := range map[string]PREvent{"c": PRComment, "r": PRRequestChanges} {
		stub := &prReviewerStub{}
		m := newPRModel(t, stub, annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "x"})
		next, _ := m.Update(keyRunes("q"))
		m = next.(Model)
		next, cmd := m.Update(keyRunes(key))
		m = next.(Model)
		require.NotNil(t, cmd, key)
		cmd()
		assert.Equal(t, []PREvent{want}, stub.submitted)
		assert.True(t, m.pr.submitting)
	}
}

func TestModel_PRQuitSkipAndEsc(t *testing.T) {
	stub := &prReviewerStub{}
	m := newPRModel(t, stub, annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "x"})
	next, _ := m.Update(keyRunes("q"))
	m = next.(Model)
	// esc closes the menu and stays in the session
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	assert.Nil(t, cmd)
	assert.False(t, m.overlay.Active())
	assert.Empty(t, stub.submitted)

	// s quits without posting. the local output is still produced
	next, _ = m.Update(keyRunes("q"))
	m = next.(Model)
	next, cmd = m.Update(keyRunes("s"))
	m = next.(Model)
	assert.True(t, quitRequested(cmd))
	assert.True(t, m.pr.skip)
	assert.Nil(t, m.PRSubmission())
	assert.Empty(t, stub.submitted)
	assert.False(t, m.Discarded())
}

func TestModel_PRSubmitFailureKeepsSession(t *testing.T) {
	stub := &prReviewerStub{err: errors.New("gh api: HTTP 422\nValidation Failed")}
	m := newPRModel(t, stub, annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "x"})
	next, _ := m.Update(keyRunes("q"))
	m = next.(Model)
	next, cmd := m.Update(keyRunes("c"))
	m = next.(Model)
	next, cmd = m.Update(cmd())
	m = next.(Model)
	assert.False(t, quitRequested(cmd))
	assert.False(t, m.pr.submitting)
	require.True(t, m.overlay.Active())
	assert.Equal(t, overlay.KindError, m.overlay.Kind())
	assert.Contains(t, m.View(), "review not posted")
	assert.Contains(t, m.View(), "HTTP 422")
	assert.Nil(t, m.PRSubmission())

	// q again re-opens the menu (after the popup is dismissed)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	next, _ = m.Update(keyRunes("q"))
	m = next.(Model)
	assert.Equal(t, overlay.KindMenu, m.overlay.Kind())
}

func TestModel_PRQuitWithoutAnnotationsStillOffersAVerdict(t *testing.T) {
	// a review that found nothing is an approval, not a silent exit
	stub := &prReviewerStub{}
	m := newPRModel(t, stub)
	next, cmd := m.Update(keyRunes("q"))
	m = next.(Model)
	assert.Nil(t, cmd, "quit is intercepted")
	require.Equal(t, overlay.KindMenu, m.overlay.Kind())
	view := m.View()
	assert.Contains(t, view, "nothing to post")
	assert.Contains(t, view, "Approve")
	assert.Contains(t, view, "Request changes")
	assert.NotContains(t, view, "[c] Comment", "both services reject a comment with no body and no comments")
	assert.Contains(t, view, "Quit without a verdict")

	next, cmd = m.Update(keyRunes("a"))
	m = next.(Model)
	require.NotNil(t, cmd)
	msg := cmd()
	assert.Equal(t, []PREvent{PRApprove}, stub.submitted)
	assert.Empty(t, stub.annots, "nothing is posted with it")
	next, cmd = m.Update(msg)
	assert.True(t, quitRequested(cmd))
	require.NotNil(t, next.(Model).PRSubmission())
}

func TestModel_PRQuitWithNoVerdictOnOffer(t *testing.T) {
	// an instance that cannot approve and cannot request changes has nothing to
	// ask about when there is nothing to post
	stub := &prReviewerStub{verdicts: []PREvent{PRComment}}
	m := newPRModel(t, stub)
	_, cmd := m.Update(keyRunes("q"))
	assert.True(t, quitRequested(cmd), "only the way out was left, so q means out")
	assert.Empty(t, stub.submitted)
}

func TestModel_PRQuitWithoutReviewer(t *testing.T) {
	stub := &prReviewerStub{}
	plain := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	plain.store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "x"})
	_, cmd := plain.Update(keyRunes("q"))
	assert.True(t, quitRequested(cmd), "no reviewer: plain quit")

	// discard-quit never asks
	m := newPRModel(t, stub, annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "x"})
	m.session.noConfirmDiscard = true
	_, cmd = m.Update(keyRunes("Q"))
	assert.True(t, quitRequested(cmd))
	assert.Empty(t, stub.submitted)
}

func TestNewModel_TypedNilPRReviewer(t *testing.T) {
	var stub *prReviewerStub
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{PRReview: stub})
	assert.Nil(t, m.pr.reviewer)
}

func TestReviewHeaderText_Label(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{ReviewInfo: &ReviewInfoConfig{Label: "PR #12: fix parser", Ref: "a..b"}})
	assert.Equal(t, "PR #12: fix parser", m.reviewHeaderText())
}
