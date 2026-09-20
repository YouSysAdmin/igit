package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/stageplan"
	"github.com/yousysadmin/igit/internal/tui/mocks"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/style"
	"github.com/yousysadmin/igit/internal/tui/worddiff"
)

// testModelConfig returns a ModelConfig with the same defaults testNewModel fills in.
func testModelConfig(store *annot.Store) ModelConfig {
	res := style.PlainResolver()
	return ModelConfig{
		DiffSource:    plainRenderer(),
		Store:         store,
		Highlighter:   noopHighlighter(),
		StyleResolver: res,
		StyleRenderer: style.NewRenderer(res),
		SGR:           style.SGR{},
		WordDiffer:    worddiff.New(),
		NewFileTree:   testFileTreeFactory(),
		Overlay:       overlay.NewManager(),
		Themes:        fakeThemeCatalog{},
	}
}

func testCommitConfig() *CommitConfig {
	return &CommitConfig{
		Repo:     &mocks.RepoMock{},
		DiffPane: testModelConfig(annot.NewStore()),
		Keymap:   keymap.DefaultCommit(),
		Overlay:  overlay.NewManager(),
	}
}

// altG is the default mode-toggle key (alt+g. ctrl+g is taken by zellij).
func altG() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}, Alt: true} }

func testApp(t *testing.T, commit *CommitConfig, start Mode) App {
	t.Helper()
	app, err := NewApp(AppConfig{Review: testModelConfig(annot.NewStore()), Commit: commit, StartMode: start})
	require.NoError(t, err)
	return app
}

func updateApp(t *testing.T, app App, msg tea.Msg) (App, tea.Cmd) {
	t.Helper()
	next, cmd := app.Update(msg)
	got, ok := next.(App)
	require.True(t, ok)
	return got, cmd
}

func TestNewApp_commitUnavailableFallsBackToReview(t *testing.T) {
	app := testApp(t, nil, ModeCommit)
	assert.Equal(t, ModeReview, app.Mode())
	assert.Nil(t, app.commit)
	assert.NotEmpty(t, app.unavailable)
}

func TestNewApp_propagatesReviewConfigError(t *testing.T) {
	_, err := NewApp(AppConfig{Review: ModelConfig{}})
	require.Error(t, err)
}

func TestApp_Init_startsOnlyActiveMode(t *testing.T) {
	review := testApp(t, testCommitConfig(), ModeReview)
	assert.NotNil(t, review.Init(), "review Init loads files")

	commit := testApp(t, testCommitConfig(), ModeCommit)
	assert.Equal(t, ModeCommit, commit.Mode())
	assert.NotNil(t, commit.Init(), "commit mode loads the status")
}

func TestApp_WindowSize_reachesBothModels(t *testing.T) {
	app := testApp(t, testCommitConfig(), ModeReview)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 120, Height: 40})
	assert.True(t, app.review.ready)
	assert.True(t, app.commit.ready)
	assert.Equal(t, 120, app.commit.width)
	assert.Equal(t, 39, app.commit.height, "the mode bar takes one row")
	require.NotNil(t, app.size)
}

func TestApp_ToggleMode_switchesAndRefreshes(t *testing.T) {
	app := testApp(t, testCommitConfig(), ModeReview)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})

	app, cmd := updateApp(t, app, altG())
	assert.Equal(t, ModeCommit, app.Mode())
	assert.True(t, app.commitInited)
	assert.NotNil(t, cmd, "entering commit mode loads the status")
	assert.Contains(t, app.View(), "Files")

	app, cmd = updateApp(t, app, altG())
	assert.Equal(t, ModeReview, app.Mode())
	assert.NotNil(t, cmd, "returning to review re-fetches the file list")
	assert.Contains(t, app.View(), "loading files...", "reload marks files as not loaded")
}

func TestNewApp_commitStartFallsBackToReview(t *testing.T) {
	app, err := NewApp(AppConfig{
		Review:      testModelConfig(annot.NewStore()),
		StartMode:   ModeCommit,
		Unavailable: "commit mode requires a git repository",
	})
	require.NoError(t, err)
	assert.Equal(t, ModeReview, app.Mode(), "an unavailable commit mode does not stop the session")
	assert.Equal(t, "commit mode requires a git repository, started in review mode", app.review.transientHint())
}

func TestApp_ToggleMode_unavailableShowsHint(t *testing.T) {
	app, err := NewApp(AppConfig{Review: testModelConfig(annot.NewStore()), Unavailable: "commit mode requires a git repository"})
	require.NoError(t, err)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	app, cmd := updateApp(t, app, altG())
	assert.Nil(t, cmd)
	assert.Equal(t, ModeReview, app.Mode())
	assert.Equal(t, "commit mode requires a git repository", app.review.transientHint())
}

func TestApp_ToggleKey_notInterceptedWhileReviewInputBusy(t *testing.T) {
	app := testApp(t, testCommitConfig(), ModeReview)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	app.review.search.active = true
	assert.True(t, app.activeBusy())
	app, _ = updateApp(t, app, altG())
	assert.Equal(t, ModeReview, app.Mode(), "modal keeps the key")
}

func TestApp_ToggleKey_notInterceptedWhileCommitOverlayOpen(t *testing.T) {
	app := testApp(t, testCommitConfig(), ModeCommit)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	app, _ = updateApp(t, app, keyRunes("?"))
	assert.True(t, app.commit.overlay.Active())
	app, _ = updateApp(t, app, altG())
	assert.Equal(t, ModeCommit, app.Mode())
	app, _ = updateApp(t, app, tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, app.commit.overlay.Active())
	app, _ = updateApp(t, app, altG())
	assert.Equal(t, ModeReview, app.Mode())
}

func TestApp_ToggleKey_customBinding(t *testing.T) {
	cfg := testModelConfig(annot.NewStore())
	km := keymap.Default()
	km.Unbind("alt+g")
	km.Bind("ctrl+t", keymap.ActionToggleMode)
	cfg.Keymap = km
	app, err := NewApp(AppConfig{Review: cfg, Commit: testCommitConfig()})
	require.NoError(t, err)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	app, _ = updateApp(t, app, altG())
	assert.Equal(t, ModeReview, app.Mode(), "unbound default does nothing")
	app, _ = updateApp(t, app, tea.KeyMsg{Type: tea.KeyCtrlT})
	assert.Equal(t, ModeCommit, app.Mode())
}

func TestApp_ToggleKey_chordLeaderIsDelegated(t *testing.T) {
	cfg := testModelConfig(annot.NewStore())
	km := keymap.Default()
	km.Bind("alt+g>x", keymap.ActionQuit)
	cfg.Keymap = km
	app, err := NewApp(AppConfig{Review: cfg, Commit: testCommitConfig()})
	require.NoError(t, err)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	app, _ = updateApp(t, app, altG())
	assert.Equal(t, ModeReview, app.Mode(), "chord leader belongs to the review model")
}

func TestApp_ForeignMessagesBroadcast(t *testing.T) {
	app := testApp(t, testCommitConfig(), ModeCommit)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	// a review-owned message lands on the review model even while commit is active
	app, _ = updateApp(t, app, filesLoadedMsg{seq: app.review.filesLoadSeq})
	assert.True(t, app.review.filesLoaded)
}

func TestApp_QuitPassesThrough(t *testing.T) {
	app := testApp(t, testCommitConfig(), ModeCommit)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	_, cmd := updateApp(t, app, keyRunes("q"))
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestApp_ReviewAccessor(t *testing.T) {
	store := annot.NewStore()
	app, err := NewApp(AppConfig{Review: testModelConfig(store)})
	require.NoError(t, err)
	assert.Same(t, store, app.Review().Store())
}

func TestApp_StyleSyncAfterThemeChange(t *testing.T) {
	app := testApp(t, testCommitConfig(), ModeReview)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})
	res := style.NewResolver(style.Colors{Accent: "#ff0000"})
	app.review.resolver = res
	app.review.renderer = style.NewRenderer(res)
	app.review.styleGen++
	app, _ = updateApp(t, app, keyRunes("j"))
	assert.Equal(t, app.review.styleGen, app.commit.styleGen)
	assert.Equal(t, res.Color(style.ColorKeyAccentFg), app.commit.diff.resolver.Color(style.ColorKeyAccentFg))
}

func TestMode_StringAndParse(t *testing.T) {
	assert.Equal(t, "review", ModeReview.String())
	assert.Equal(t, "commit", ModeCommit.String())
	assert.Equal(t, "unknown", Mode(42).String())
	m, ok := ParseMode("commit")
	assert.True(t, ok)
	assert.Equal(t, ModeCommit, m)
	m, ok = ParseMode("")
	assert.True(t, ok)
	assert.Equal(t, ModeReview, m)
	_, ok = ParseMode("nope")
	assert.False(t, ok)
}

func TestModel_inputBusy(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	assert.False(t, m.inputBusy())
	cases := []func(*Model){
		func(m *Model) { m.annot.annotating = true },
		func(m *Model) { m.search.active = true },
		func(m *Model) { m.output.saving = true },
		func(m *Model) { m.inConfirmDiscard = true },
		func(m *Model) { m.reload.pending = true },
		func(m *Model) { m.keys.chordPending = "ctrl+w" },
		func(m *Model) { m.overlay.OpenHelp(overlay.HelpSpec{}) },
	}
	for i, set := range cases {
		mm := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
		set(&mm)
		assert.True(t, mm.inputBusy(), "case %d", i)
	}
}

func TestModel_SetHintAndRefreshCmd(t *testing.T) {
	m := testNewModel(t, plainRenderer(), annot.NewStore(), noopHighlighter(), ModelConfig{})
	m.SetHint("hello")
	assert.Equal(t, "hello", m.transientHint())
	m.store.Add(annot.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "keep"})
	cmd := m.RefreshCmd()
	assert.NotNil(t, cmd)
	assert.Equal(t, 1, m.store.Count(), "refresh keeps annotations")
}

type plannerStub struct {
	result stageplan.Result
	err    error
	calls  int
}

func (p *plannerStub) ApplyPlan(_ context.Context, _ *stageplan.Plan) (stageplan.Result, error) {
	p.calls++
	return p.result, p.err
}

func planApp(t *testing.T, planner PlanApplier) App {
	t.Helper()
	cfg := testModelConfig(annot.NewStore())
	cfg.Applicable.StagePlan = true
	commit := testCommitConfig()
	commit.Repo = newRepoMock(sampleStatus())
	app, err := NewApp(AppConfig{Review: cfg, Commit: commit, PlanApplier: planner})
	require.NoError(t, err)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 120, Height: 40})
	return app
}

func TestApp_CommitWithPlan_emptyPlanJustSwitches(t *testing.T) {
	planner := &plannerStub{}
	app := planApp(t, planner)
	app, cmd := updateApp(t, app, commitWithPlanMsg{plan: stageplan.New()})
	assert.Equal(t, ModeCommit, app.Mode())
	assert.NotNil(t, cmd)
	assert.Equal(t, 0, planner.calls)
}

func TestApp_CommitWithPlan_appliesAndFocusesStaged(t *testing.T) {
	planner := &plannerStub{result: stageplan.Result{Staged: []string{"staged.go"}, Skipped: []stageplan.Skipped{{Path: "b.go", Reason: "diff changed"}}}}
	app := planApp(t, planner)
	plan := app.review.StagePlan()
	plan.ToggleFile("staged.go")
	plan.ToggleFile("b.go")

	app, cmd := updateApp(t, app, commitWithPlanMsg{plan: plan})
	require.NotNil(t, cmd)
	assert.Equal(t, ModeReview, app.Mode(), "mode switches only after the plan ran")
	msg := cmd()
	require.IsType(t, planAppliedMsg{}, msg)
	assert.Equal(t, 1, planner.calls)

	app, cmd = updateApp(t, app, msg)
	assert.Equal(t, ModeCommit, app.Mode())
	require.NotNil(t, cmd, "commit mode refreshes its status")
	assert.False(t, plan.Has("staged.go"), "staged files leave the plan")
	assert.True(t, plan.Has("b.go"), "skipped files stay marked")
	assert.Contains(t, app.commit.hint, "staged 1 marked file(s), 1 skipped")
	assert.Equal(t, overlay.KindError, app.commit.overlay.Kind())
	assert.Contains(t, app.commit.View(), "b.go: diff changed")

	// once the status lands, the cursor sits on the first staged row
	app.commit.overlay.Close()
	app.commit.Update(commitStatusMsg{seq: app.commit.statusSeq, status: sampleStatus()})
	fr, ok := app.commit.cursorFile()
	require.True(t, ok)
	assert.True(t, fr.staged)
	assert.Equal(t, "both.go", fr.file.Path)
}

func TestApp_CommitWithPlan_errorsAndMissingPlanner(t *testing.T) {
	planner := &plannerStub{err: errors.New("status broke")}
	app := planApp(t, planner)
	plan := app.review.StagePlan()
	plan.ToggleFile("a.go")
	app, cmd := updateApp(t, app, commitWithPlanMsg{plan: plan})
	app, _ = updateApp(t, app, cmd())
	assert.Equal(t, ModeReview, app.Mode())
	assert.Contains(t, app.review.transientHint(), "status broke")
	assert.True(t, plan.Has("a.go"))

	noPlanner := planApp(t, nil)
	noPlanner.review.StagePlan().ToggleFile("a.go")
	noPlanner, cmd = updateApp(t, noPlanner, commitWithPlanMsg{plan: noPlanner.review.StagePlan()})
	assert.Nil(t, cmd)
	assert.Contains(t, noPlanner.review.transientHint(), "cannot be applied")

	// commit mode unavailable: the hint explains
	cfg := testModelConfig(annot.NewStore())
	cfg.Applicable.StagePlan = true
	solo, err := NewApp(AppConfig{Review: cfg, Unavailable: "commit mode requires a git repository"})
	require.NoError(t, err)
	solo, cmd = updateApp(t, solo, commitWithPlanMsg{plan: stageplan.New()})
	assert.Nil(t, cmd)
	assert.Contains(t, solo.review.transientHint(), "requires a git repository")
}

func TestApp_PlanAppliedWhileAlreadyInCommitMode(t *testing.T) {
	app := planApp(t, &plannerStub{})
	app, _ = updateApp(t, app, altG())
	require.Equal(t, ModeCommit, app.Mode())
	app, cmd := updateApp(t, app, planAppliedMsg{result: stageplan.Result{Staged: []string{"x"}}})
	assert.NotNil(t, cmd)
	assert.Contains(t, app.commit.hint, "staged 1 marked file(s)")
	assert.Equal(t, "staged 2 marked file(s)", planSummary(stageplan.Result{Staged: []string{"a", "b"}}))
	assert.Equal(t, "a: r1\nb: r2\n", skippedDetail(stageplan.Result{Skipped: []stageplan.Skipped{{Path: "a", Reason: "r1"}, {Path: "b", Reason: "r2"}}}))
}

func TestApp_ModeBar(t *testing.T) {
	app := testApp(t, testCommitConfig(), ModeReview)
	assert.NotContains(t, app.View(), "Review", "no bar before the first size")
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 120, Height: 40})
	assert.Equal(t, 39, app.review.layout.height, "models get the window minus the bar")
	assert.Equal(t, 39, app.commit.height)
	view := app.View()
	first, _, _ := strings.Cut(view, "\n")
	assert.Contains(t, first, "Review")
	assert.Contains(t, first, "Commit")
	assert.Contains(t, first, "alt+g: switch mode")

	// clicking the Commit tab switches, clicking the active tab is a no-op
	tabs := modeTabs()
	app, cmd := updateApp(t, app, click(tabs[1].from+1, 0))
	assert.Equal(t, ModeCommit, app.Mode())
	assert.NotNil(t, cmd)
	app, cmd = updateApp(t, app, click(tabs[1].from+1, 0))
	assert.Equal(t, ModeCommit, app.Mode())
	assert.Nil(t, cmd)
	app, _ = updateApp(t, app, click(tabs[0].from, 0))
	assert.Equal(t, ModeReview, app.Mode())
	// wheel and clicks between tabs are swallowed
	app, cmd = updateApp(t, app, wheel(5, 0, true))
	assert.Nil(t, cmd)
	app, cmd = updateApp(t, app, click(0, 0))
	assert.Nil(t, cmd)
	assert.Equal(t, ModeReview, app.Mode())

	// other mouse events are shifted up one row before reaching the model:
	// row 1 of the screen is the review model's top border, so nothing happens
	app, cmd = updateApp(t, app, click(tabs[0].from, 1))
	assert.Nil(t, cmd)

	// unavailable commit mode: the tab is drawn but clicking only hints
	solo, err := NewApp(AppConfig{Review: testModelConfig(annot.NewStore()), Unavailable: "commit mode requires a git repository"})
	require.NoError(t, err)
	solo, _ = updateApp(t, solo, tea.WindowSizeMsg{Width: 120, Height: 40})
	assert.Contains(t, solo.View(), "Commit")
	solo, _ = updateApp(t, solo, click(tabs[1].from+1, 0))
	assert.Equal(t, ModeReview, solo.Mode())
	assert.Contains(t, solo.review.transientHint(), "requires a git repository")

	// narrow window drops the key hint but keeps the tabs
	narrow := app.modeBar(20)
	assert.Contains(t, narrow, "Review")
	assert.NotContains(t, narrow, "switch mode")
	km := keymap.Default()
	km.Unbind("alt+g")
	assert.Empty(t, toggleKeyHint(km))
}

// pickThemeCatalog resolves one theme so the selector can confirm it.
type pickThemeCatalog struct{ persisted string }

func (pickThemeCatalog) Entries() ([]ThemeEntry, error) {
	return []ThemeEntry{{Name: "picked", AccentColor: "#ff0000"}}, nil
}

func (pickThemeCatalog) Resolve(name string) (ThemeSpec, bool) {
	if name != "picked" {
		return ThemeSpec{}, false
	}
	return ThemeSpec{Colors: style.Colors{Accent: "#ff0000"}}, true
}
func (p *pickThemeCatalog) Persist(name string) error { p.persisted = name; return nil }

func TestApp_ThemePickedInCommitModeReachesReview(t *testing.T) {
	cat := &pickThemeCatalog{}
	review := testModelConfig(annot.NewStore())
	review.Themes = cat
	commit := testCommitConfig()
	commit.DiffPane.Themes = cat
	app, err := NewApp(AppConfig{Review: review, Commit: commit, StartMode: ModeCommit})
	require.NoError(t, err)
	app, _ = updateApp(t, app, tea.WindowSizeMsg{Width: 100, Height: 30})

	app, _ = updateApp(t, app, keyRunes("T"))
	require.True(t, app.commit.overlay.Active())
	assert.Equal(t, overlay.KindThemeSelect, app.commit.overlay.Kind())
	assert.True(t, app.commit.inputBusy())
	assert.Contains(t, app.View(), "picked")

	app, _ = updateApp(t, app, tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, app.commit.overlay.Active())
	assert.Equal(t, "picked", cat.persisted)
	want := style.NewResolver(style.Colors{Accent: "#ff0000"}).Color(style.ColorKeyAccentFg)
	assert.Equal(t, want, app.commit.diff.resolver.Color(style.ColorKeyAccentFg))
	assert.Equal(t, want, app.review.resolver.Color(style.ColorKeyAccentFg), "the review model adopts the theme")
	assert.Equal(t, "picked", app.review.activeThemeName)
	assert.Equal(t, app.review.styleGen, app.commit.styleGen, "generations are in sync afterwards")

	// esc cancels: the styling is restored on both sides
	app, _ = updateApp(t, app, keyRunes("T"))
	app, _ = updateApp(t, app, tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, app.commit.overlay.Active())
	assert.Equal(t, want, app.review.resolver.Color(style.ColorKeyAccentFg))
}

func TestNewApp_startNoteReachesTheOpeningMode(t *testing.T) {
	app, err := NewApp(AppConfig{
		Review:    testModelConfig(annot.NewStore()),
		Commit:    testCommitConfig(),
		StartMode: ModeCommit,
		StartNote: "merging, started in commit mode where it is finished",
	})
	require.NoError(t, err)
	require.Equal(t, ModeCommit, app.Mode())
	assert.Equal(t, "merging, started in commit mode where it is finished", app.commit.hint)
	assert.Empty(t, app.review.transientHint(), "the note goes to the mode that opened")

	// a review start puts it in the review status bar instead
	app, err = NewApp(AppConfig{
		Review: testModelConfig(annot.NewStore()), Commit: testCommitConfig(),
		StartMode: ModeReview, StartNote: "a note",
	})
	require.NoError(t, err)
	assert.Equal(t, "a note", app.review.transientHint())
}
