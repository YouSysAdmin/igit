package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"

	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/stageplan"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/style"
)

// modeBarHeight is the row the mode tabs occupy above the active model.
const modeBarHeight = 1

// modeTab describes one clickable tab of the mode bar: its label and the
// half-open column span it occupies.
type modeTab struct {
	mode  Mode
	label string
	from  int
	to    int
}

// modeTabs returns the tabs with their column spans: " Review " then " Commit ",
// separated by one space, starting at column 1.
func modeTabs() []modeTab {
	tabs := []modeTab{{mode: ModeReview, label: " Review "}, {mode: ModeCommit, label: " Commit "}}
	x := 1
	for i := range tabs {
		tabs[i].from = x
		tabs[i].to = x + len(tabs[i].label)
		x = tabs[i].to + 1
	}
	return tabs
}

// PlanApplier stages a review stage plan. implemented by *gitops.Git.
type PlanApplier interface {
	ApplyPlan(ctx context.Context, plan *stageplan.Plan) (stageplan.Result, error)
}

// planAppliedMsg reports a finished plan run.
type planAppliedMsg struct {
	result stageplan.Result
	err    error
}

// App is the root bubbletea model. It owns the active Mode and delegates
// input and rendering to the review Model and the CommitModel, sharing the
// annotation store and the theme between them. Messages that are not keyboard
// or mouse input are broadcast to both sub-models: each one ignores message
// types it does not own, so an async load that completes after a mode switch
// still lands on the model that requested it.
type App struct {
	mode         Mode
	review       Model
	commit       *CommitModel // nil when commit mode is unavailable
	planner      PlanApplier  // applies review stage marks, nil = plans cannot be applied
	unavailable  string       // status hint shown when commit mode cannot be entered
	size         *tea.WindowSizeMsg
	commitInited bool
	reviewInited bool
}

// AppConfig configures the root model. Commit may be nil, in which case the
// mode toggle shows Unavailable as a status hint instead of switching.
type AppConfig struct {
	Review      ModelConfig
	Commit      *CommitConfig
	PlanApplier PlanApplier // nil disables applying review stage marks
	Unavailable string
	StartMode   Mode
	// StartNote explains a start mode the flags did not ask for. It reaches the
	// status bar of whichever mode the session opens in.
	StartNote string
}

// NewApp builds the root model and both sub-models.
func NewApp(cfg AppConfig) (App, error) {
	review, err := NewModel(cfg.Review)
	if err != nil {
		return App{}, err
	}
	app := App{mode: cfg.StartMode, review: review, unavailable: cfg.Unavailable}
	if !depMissing(cfg.PlanApplier) {
		app.planner = cfg.PlanApplier
	}
	if cfg.Commit != nil {
		commit, err := NewCommitModel(*cfg.Commit)
		if err != nil {
			return App{}, err
		}
		app.commit = commit
	}
	if app.unavailable == "" {
		app.unavailable = "commit mode is not available in this session"
	}
	// a commit start mode can come from an alias or the config file. falling
	// back is not an error, the status bar carries the reason
	if app.mode == ModeCommit && app.commit == nil {
		app.mode = ModeReview
		app.review.SetHint(app.unavailable + ", started in review mode")
	}
	if cfg.StartNote != "" {
		if app.mode == ModeCommit && app.commit != nil {
			app.commit.hint = cfg.StartNote
		} else {
			app.review.SetHint(cfg.StartNote)
		}
	}
	// Init runs for the start mode only. the other mode loads on first switch.
	app.commitInited = app.mode == ModeCommit
	app.reviewInited = app.mode == ModeReview
	return app, nil
}

// Init starts only the active mode. the other one loads lazily on first switch.
func (a App) Init() tea.Cmd {
	if a.mode == ModeCommit && a.commit != nil {
		return a.commit.Init()
	}
	return a.review.Init()
}

// Update routes messages: window size to both models, keys and mouse to the
// active one (after intercepting the mode toggle), everything else to both.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.size = &msg
		// the mode bar takes the top row. the models get the rest
		inner := tea.WindowSizeMsg{Width: msg.Width, Height: max(msg.Height-modeBarHeight, 1)}
		var cmds []tea.Cmd
		a.review, cmds = a.updateReview(inner, cmds)
		if a.commit != nil {
			cmds = append(cmds, a.commit.Update(inner))
		}
		return a, tea.Batch(cmds...)
	case tea.KeyMsg:
		if !a.activeBusy() && a.isToggleKey(msg) {
			return a.toggleMode()
		}
		return a.updateActive(msg)
	case tea.MouseMsg:
		if msg.Y < modeBarHeight {
			return a.clickModeBar(msg)
		}
		msg.Y -= modeBarHeight
		return a.updateActive(msg)
	case commitWithPlanMsg:
		return a.applyPlan(msg.plan)
	case planAppliedMsg:
		return a.planApplied(msg)
	}
	var cmds []tea.Cmd
	a.review, cmds = a.updateReview(msg, cmds)
	if a.commit != nil {
		cmds = append(cmds, a.commit.Update(msg))
	}
	return a, tea.Batch(cmds...)
}

// View renders the mode bar above the active mode.
func (a App) View() string {
	body := a.review.View()
	if a.mode == ModeCommit && a.commit != nil {
		body = a.commit.View()
	}
	if a.size == nil {
		return body
	}
	return a.modeBar(a.size.Width) + "\n" + body
}

// modeBar renders the clickable Review / Commit tabs in the review theme. an
// unavailable commit mode is drawn muted. The right edge names the toggle key.
func (a App) modeBar(width int) string {
	res := a.review.resolver
	active := res.Style(style.StyleKeyFileSelected)
	inactive := res.Style(style.StyleKeyFileEntry)
	var b strings.Builder
	b.WriteString(" ")
	for i, tab := range modeTabs() {
		if i > 0 {
			b.WriteString(" ")
		}
		switch {
		case tab.mode == a.mode:
			b.WriteString(active.Render(tab.label))
		case tab.mode == ModeCommit && a.commit == nil:
			b.WriteString(string(res.Color(style.ColorKeyMutedFg)) + tab.label + string(style.ResetFg))
		default:
			b.WriteString(inactive.Render(tab.label))
		}
	}
	line := b.String()
	hint := string(res.Color(style.ColorKeyMutedFg)) + toggleKeyHint(a.activeKeymap()) + string(style.ResetFg)
	pad := width - lipgloss.Width(line) - lipgloss.Width(hint) - 1
	if pad < 1 {
		return line
	}
	return line + strings.Repeat(" ", pad) + hint
}

// toggleKeyHint names the first key bound to toggle_mode, e.g. "alt+g: switch mode".
func toggleKeyHint(km *keymap.Keymap) string {
	keys := km.KeysFor(keymap.ActionToggleMode)
	if len(keys) == 0 {
		return ""
	}
	return keymap.DisplayKey(keys[0]) + ": switch mode"
}

// activeKeymap returns the keymap of the active mode.
func (a App) activeKeymap() *keymap.Keymap {
	if a.mode == ModeCommit && a.commit != nil {
		return a.commit.keymap
	}
	return a.review.keymap
}

// clickModeBar switches modes when a tab is clicked. every other mouse event
// on the bar is swallowed.
func (a App) clickModeBar(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return a, nil
	}
	for _, tab := range modeTabs() {
		if msg.X >= tab.from && msg.X < tab.to && tab.mode != a.mode {
			return a.toggleMode()
		}
	}
	return a, nil
}

// Review returns the review model, whose store and output path the composition
// root reads after the program exits.
func (a App) Review() Model { return a.review }

// Mode returns the active mode.
func (a App) Mode() Mode { return a.mode }

// updateReview runs one review update, keeps the commit model's styling in
// sync (theme changes happen in review mode) and appends the resulting cmd.
func (a *App) updateReview(msg tea.Msg, cmds []tea.Cmd) (Model, []tea.Cmd) {
	next, cmd := a.review.Update(msg)
	review, ok := next.(Model)
	if !ok {
		return a.review, cmds
	}
	if a.commit != nil {
		a.commit.setStyle(review.styleGen, review.resolver, review.renderer, review.sgr, review.highlighter)
	}
	return review, append(cmds, cmd)
}

func (a App) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	if a.mode == ModeCommit && a.commit != nil {
		cmd := a.commit.Update(msg)
		if a.commit.styleChanged() {
			// a theme picked in commit mode becomes the session theme
			a.review.adoptStyle(a.commit.diff)
			a.commit.styleGen = a.review.styleGen
		}
		return a, cmd
	}
	var cmds []tea.Cmd
	a.review, cmds = a.updateReview(msg, cmds)
	return a, tea.Batch(cmds...)
}

// activeBusy reports whether the active model is inside a modal (text input,
// confirmation, overlay, pending chord) that must receive every key verbatim.
func (a App) activeBusy() bool {
	if a.mode == ModeCommit && a.commit != nil {
		return a.commit.inputBusy()
	}
	return a.review.inputBusy()
}

func (a App) isToggleKey(msg tea.KeyMsg) bool {
	km := a.review.keymap
	if a.mode == ModeCommit && a.commit != nil {
		km = a.commit.keymap
	}
	key := msg.String()
	if km.IsChordLeader(key) {
		return false
	}
	return km.Resolve(key) == keymap.ActionToggleMode
}

// applyPlan stages the review marks and then enters commit mode. An empty plan
// is a plain switch. without a planner the marks stay and a hint says so.
func (a App) applyPlan(plan *stageplan.Plan) (tea.Model, tea.Cmd) {
	if a.commit == nil {
		a.review.SetHint(a.unavailable)
		return a, nil
	}
	if plan == nil || plan.Empty() {
		return a.toggleMode()
	}
	if a.planner == nil {
		a.review.SetHint("stage marks cannot be applied in this session")
		return a, nil
	}
	planner := a.planner
	return a, func() tea.Msg {
		res, err := planner.ApplyPlan(context.Background(), plan)
		return planAppliedMsg{result: res, err: err}
	}
}

// planApplied reports the run, drops the staged files from the plan and
// switches to commit mode focused on the Staged section.
func (a App) planApplied(msg planAppliedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.review.SetHint("staging marked changes failed: " + msg.err.Error())
		return a, nil
	}
	a.review.removeStagedFromPlan(msg.result)
	if a.mode == ModeReview {
		next, cmd := a.toggleMode()
		app, ok := next.(App)
		if !ok {
			return next, cmd
		}
		a = app
		a.commit.focusStagedOnLoad = true
		a.commit.hint = planSummary(msg.result)
		if len(msg.result.Skipped) > 0 {
			a.commit.overlay.OpenError(overlay.ErrorSpec{Title: "some marks were not staged", Summary: planSummary(msg.result), Detail: skippedDetail(msg.result)})
		}
		return a, cmd
	}
	a.commit.hint = planSummary(msg.result)
	return a, a.commit.Refresh()
}

func planSummary(res stageplan.Result) string {
	s := fmt.Sprintf("staged %d marked file(s)", len(res.Staged))
	if n := len(res.Skipped); n > 0 {
		s += fmt.Sprintf(", %d skipped (still marked in review mode)", n)
	}
	return s
}

func skippedDetail(res stageplan.Result) string {
	var b strings.Builder
	for _, sk := range res.Skipped {
		fmt.Fprintf(&b, "%s: %s\n", sk.Path, sk.Reason)
	}
	return b.String()
}

// toggleMode switches between review and commit. Entering commit mode runs its
// Init on first use and a refresh afterwards. returning to review re-fetches
// the file list (staging may have changed the working tree) without dropping
// annotations.
func (a App) toggleMode() (tea.Model, tea.Cmd) {
	if a.commit == nil {
		a.review.SetHint(a.unavailable)
		return a, nil
	}
	if a.mode == ModeReview {
		a.mode = ModeCommit
		var cmd tea.Cmd
		if a.commitInited {
			cmd = a.commit.Refresh()
		} else {
			a.commitInited = true
			cmd = a.commit.Init()
		}
		return a, cmd
	}
	a.mode = ModeReview
	a.reviewInited = true
	return a, a.review.RefreshCmd()
}
