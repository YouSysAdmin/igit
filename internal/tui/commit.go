package tui

import (
	"errors"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
)

// processRunner launches an interactive command (editor, git commit, push)
// and delivers done(err) as a message when it exits. tea.ExecProcess in
// production. tests inject a stub.
type processRunner func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd

// CommitConfig configures the commit-mode model.
type CommitConfig struct {
	Repo Repo // git write layer, required

	// DiffPane configures the embedded review Model that renders the staging
	// diff. The composition root passes the review ModelConfig so display
	// settings (wrap, line numbers, word diff, tab width, start-at-change,
	// cross-file hunks, theme) behave the same in both modes. Review-only
	// concerns are overridden here: the tree and status bar are hidden, the
	// overlay manager is shared with the commit model, and collapsed/compact
	// views, blame and the info popup are disabled.
	DiffPane ModelConfig

	Keymap  *keymap.Keymap // nil = keymap.DefaultCommit()
	Overlay overlayManager // nil = overlay.NewManager(), never shared with the review model
	Editor  ExternalEditor // opens files in $EDITOR, nil disables the action
	Run     processRunner  // nil = tea.ExecProcess

	// FileFilter limits the Files tab to the paths it accepts (--include,
	// --exclude, --only). nil shows every changed file. Stage-all and
	// unstage-all act on the visible files only while a filter is set.
	FileFilter func(path string) bool

	RepoRoot       string
	SideWidthRatio int  // side pane width in tenths of the window, <= 0 means defaultCommitSideRatio
	NoSidePane     bool // start with the side pane hidden (--no-tree), toggle_tree shows it
	MouseTracking  bool
	NoStatusBar    bool
	// LogDiffFiles caps how many files the Log and Stash tabs render in the
	// combined diff of the highlighted entry, so a commit touching hundreds of
	// files does not stall the pane on parsing and highlighting them all.
	// Enter still opens the full file list. 0 means no cap.
	LogDiffFiles int

	NoVerify bool // pass --no-verify to git commit
	Signoff  bool // pass --signoff to git commit
}

// commitFocus says which pane owns navigation keys.
type commitFocus int

const (
	focusSide commitFocus = iota
	focusDiff
)

// CommitModel is the git staging and commit workspace. It is driven by App,
// which owns the mode switch. CommitModel itself never implements tea.Model.
type CommitModel struct {
	repo    Repo
	keymap  *keymap.Keymap
	overlay overlayManager
	editor  ExternalEditor
	run     processRunner

	diff Model          // embedded review model used purely as the diff pane
	list *sidepane.List // side pane rows (files tab)
	tab  commitTab      // active side pane tab
	hist historyState   // branches, log and stash tabs

	status       gitops.Status
	statusLoaded bool
	statusSeq    uint64
	staging      stagingState
	focus        commitFocus
	filter       func(path string) bool // Files tab scope (--include/--exclude/--only), nil = everything

	repoRoot       string
	sideWidthRatio int
	sideHidden     bool // side pane toggled off (--no-tree / toggle_tree), the diff takes the full width
	crossFileHunks bool // ] and [ continue into the next / previous file at a diff boundary
	noStatusBar    bool
	mouseTracking  bool
	logDiffFiles   int
	noVerify       bool
	signoff        bool

	width, height int
	ready         bool
	hint          string        // transient status-bar message, cleared on next key press
	pending       pendingAction // action awaiting a confirmation popup
	styleGen      uint64        // review style generation last applied via setStyle
	diffStyleGen  uint64        // diff pane style generation last reported to App (theme picked in commit mode)

	focusStagedOnLoad bool // put the cursor on the first Staged row when the next status lands
}

// pendingAction remembers what a confirmation popup will trigger.
type pendingAction struct {
	id      string
	file    gitops.StatusEntry
	scope   gitops.DiscardScope
	indices []int  // patch line indices for a line discard
	seed    string // previous commit message for an amend prompt
	name    string // branch name or stash ref the action targets
	index   int    // stash index the action targets
}

// NewCommitModel validates the configuration and builds the model, including
// the embedded diff pane.
func NewCommitModel(cfg CommitConfig) (*CommitModel, error) {
	if depMissing(cfg.Repo) {
		return nil, errors.New("tui.NewCommitModel: Repo is required")
	}
	ov := cfg.Overlay
	if depMissing(ov) {
		ov = overlay.NewManager()
	}
	diffCfg := cfg.DiffPane
	diffCfg.NoTree = true
	diffCfg.NoStatusBar = true
	diffCfg.Overlay = ov // one popup at a time for the whole mode, the theme selector lives in the diff model
	diffCfg.Collapsed = false
	diffCfg.Compact = false
	diffCfg.Applicable.Compact = false
	diffCfg.CrossFileHunks = false // the commit model steps the side list itself
	diffCfg.ShowBlame = false
	diffCfg.Blamer = nil
	diffCfg.ReviewInfo = nil
	if depMissing(diffCfg.DiffSource) {
		diffCfg.DiffSource = noopDiffSource{}
	}
	diff, err := NewModel(diffCfg)
	if err != nil {
		return nil, err
	}
	diff.filesLoaded = true // the commit model feeds lines directly, there is no file list to wait for
	diff.layout.focus = paneDiff
	diff.embedAsPane()

	km := cfg.Keymap
	if km == nil {
		km = keymap.DefaultCommit()
	}
	run := cfg.Run
	if run == nil {
		run = func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd { return tea.ExecProcess(cmd, done) }
	}
	ratio := cfg.SideWidthRatio
	if ratio <= 0 {
		ratio = defaultCommitSideRatio
	}
	ed := cfg.Editor
	if depMissing(ed) {
		ed = nil
	}
	c := &CommitModel{
		repo:           cfg.Repo,
		keymap:         km,
		overlay:        ov,
		editor:         ed,
		run:            run,
		diff:           diff,
		list:           sidepane.NewList(),
		hist:           newHistoryState(),
		staging:        stagingState{restoreRow: -1},
		filter:         cfg.FileFilter,
		repoRoot:       cfg.RepoRoot,
		sideWidthRatio: ratio,
		sideHidden:     cfg.NoSidePane,
		crossFileHunks: cfg.DiffPane.CrossFileHunks,
		noStatusBar:    cfg.NoStatusBar,
		mouseTracking:  cfg.MouseTracking,
		logDiffFiles:   cfg.LogDiffFiles,
		noVerify:       cfg.NoVerify,
		signoff:        cfg.Signoff,
	}
	// the diff pane paints range/hunk selections through this hook
	c.diff.setPaneLineSelected(c.isRowSelected)
	if c.sideHidden {
		c.focus = focusDiff
	}
	return c, nil
}

// noopDiffSource satisfies the embedded diff model's required DiffSource. the
// commit model never asks it for anything.
type noopDiffSource struct{}

func (noopDiffSource) ChangedFiles(string, bool) ([]git.FileEntry, error) { return nil, nil }
func (noopDiffSource) FileDiff(git.FileDiffRequest) ([]git.DiffLine, error) {
	return nil, nil
}

// Init starts the first status load when the mode becomes active.
func (c *CommitModel) Init() tea.Cmd { return c.Refresh() }

// Refresh re-reads the working tree status and the active history tab. the
// current diff reloads once the data arrives.
func (c *CommitModel) Refresh() tea.Cmd {
	c.statusSeq++
	return tea.Batch(c.loadStatus(c.statusSeq), c.refreshTab())
}

// Update handles one message and returns the follow-up command.
func (c *CommitModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.resize(msg.Width, msg.Height)
		return nil
	case tea.KeyMsg:
		return c.handleKey(msg)
	case tea.MouseMsg:
		return c.handleMouse(msg)
	case wheelDebounceMsg:
		next, cmd := c.diff.handleWheelDebounce(msg)
		if m, ok := next.(Model); ok {
			c.diff = m
		}
		return cmd
	case commitStatusMsg:
		return c.handleStatusLoaded(msg)
	case commitDiffMsg:
		return c.handleDiffLoaded(msg)
	case commitOpDoneMsg:
		return c.handleOpDone(msg)
	case commitProcessDoneMsg:
		return c.handleProcessDone(msg)
	case commitPromptMsg:
		return c.handleCommitPrompt(msg)
	case commitBranchesMsg:
		return c.handleBranchesLoaded(msg)
	case commitLogMsg:
		return c.handleLogLoaded(msg)
	case commitStashesMsg:
		return c.handleStashesLoaded(msg)
	case commitDetailFilesMsg:
		return c.handleDetailFiles(msg)
	case commitRefDiffMsg:
		return c.handleRefDiff(msg)
	case commitEntryDiffMsg:
		return c.handleEntryDiff(msg)
	case syncDoneMsg:
		return c.handleSyncDone(msg)
	}
	return nil
}

// inputBusy reports whether a modal owns the keyboard: a popup or the diff
// pane's search prompt.
func (c *CommitModel) inputBusy() bool { return c.overlay.Active() || c.diff.paneSearching() }

// setStyle adopts the review model's current styling so a theme switched in
// review mode applies here as well. gen is the review model's style generation.
// nothing happens while it matches the last one applied.
func (c *CommitModel) setStyle(gen uint64, res styleResolver, ren styleRenderer, sgr sgrProcessor, hl SyntaxHighlighter) {
	if gen == c.styleGen {
		return
	}
	c.styleGen = gen
	c.diffStyleGen = c.diff.styleGen
	c.diff.resolver, c.diff.renderer, c.diff.sgr = res, ren, sgr
	if hl != nil {
		c.diff.highlighter = hl
	}
	c.diff.refreshDiff()
}

// styleChanged reports whether the diff pane picked a new theme (the theme
// selector runs inside the embedded model) since App last looked, so App can
// hand the styling to the review model.
func (c *CommitModel) styleChanged() bool {
	if c.diff.styleGen == c.diffStyleGen {
		return false
	}
	c.diffStyleGen = c.diff.styleGen
	return true
}

// buildHelpSpec converts the commit keymap's help sections into the overlay spec.
func (c *CommitModel) buildHelpSpec() overlay.HelpSpec {
	kmSections := c.keymap.HelpSections()
	sections := make([]overlay.HelpSection, 0, len(kmSections))
	for _, sec := range kmSections {
		entries := make([]overlay.HelpEntry, 0, len(sec.Entries))
		for _, e := range sec.Entries {
			entries = append(entries, overlay.HelpEntry{Keys: e.Keys, Description: e.Description})
		}
		sections = append(sections, overlay.HelpSection{Title: sec.Name, Entries: entries})
	}
	return overlay.HelpSpec{Sections: sections}
}

// depMissing reports whether an interface-typed dependency is absent: either a
// nil interface or a typed nil inside it.
func depMissing(v any) bool {
	return v == nil || isNilValue(v)
}
