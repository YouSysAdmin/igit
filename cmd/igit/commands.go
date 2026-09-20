package main

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/yousysadmin/igit/internal/extcmd"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/tui"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// commitSubcommandName is the positional word that starts the TUI in commit mode.
const commitSubcommandName = "commit"

// detectCommitSubcommand turns "igit commit [OPTIONS]" into commit mode. go-flags
// fills positional args before it considers subcommands, so the word arrives in
// Refs.Base. it is treated as the subcommand only when it stands alone and was
// not forced positional with "--" (which lets a ref named commit be reviewed).
func detectCommitSubcommand(opts options, args []string) options {
	if opts.Refs.Base != commitSubcommandName || opts.Refs.Against != "" {
		return opts
	}
	if slices.Contains(args, "--") {
		return opts
	}
	opts.commitSubcommand = true
	opts.Refs.Base = ""
	return opts
}

// startMode returns the TUI mode the session opens in. A subcommand names the
// mode itself and outranks the --mode default, which can also come from an
// alias, IGIT_MODE or the config file.
func (o options) startMode() string {
	switch {
	case o.commitSubcommand:
		return "commit"
	case o.prSubcommand:
		return "review"
	}
	return o.Mode
}

// commitUnavailableReason explains why commit mode is off for the detected
// session (a pull-request review, or no git repository), or returns "" when it
// is available.
func commitUnavailableReason(opts options, isGit bool) string {
	switch {
	case opts.prSubcommand:
		return "commit mode is not available while reviewing a pull request"
	case !isGit:
		return "commit mode requires a git repository"
	}
	return ""
}

// modeInputs carries what resolveModes needs to decide the start mode and
// build the commit-mode configuration.
type modeInputs struct {
	opts    options
	keymap  *keymap.Keymap
	review  tui.ModelConfig // the review configuration, the commit diff pane reuses its dependencies
	gitRoot string
	workDir string // where igit was started, --only patterns are relative to it
	isGit   bool
	// inProgress is the unfinished merge, rebase or cherry-pick, read once by
	// the composition root.
	inProgress gitops.InProgress
}

// modeSetup is the App-level mode configuration: the start mode, the commit
// config (nil when unavailable), the reason it is unavailable and a note
// explaining a start mode the flags did not ask for.
type modeSetup struct {
	start       tui.Mode
	commit      *tui.CommitConfig
	planner     tui.PlanApplier
	unavailable string
	startNote   string
}

// stagePlanApplicable reports whether review stage marks can be applied: a
// git working-tree review (no ref, not --staged, not stdin/compare/all-files).
func stagePlanApplicable(opts options, isGit bool) bool {
	return isGit && commitUnavailableReason(opts, isGit) == "" && opts.ref() == "" && !opts.Review.Staged
}

// resolveModes decides the start mode and builds the commit configuration. An
// unavailable commit mode is not an error: the session starts in review and the
// reason reaches the status bar.
func resolveModes(in modeInputs) modeSetup {
	unavailable := commitUnavailableReason(in.opts, in.isGit)
	start, _ := tui.ParseMode(in.opts.startMode())
	setup := modeSetup{start: start, unavailable: unavailable}
	if unavailable == "" {
		repo := gitops.New(in.gitRoot)
		setup.planner = repo
		setup.commit = &tui.CommitConfig{
			Repo:           repo,
			DiffPane:       in.review,
			Keymap:         in.keymap,
			Overlay:        overlay.NewManager(),
			Editor:         extcmd.Editor{Dir: in.gitRoot},
			FileFilter:     fileScope(in.opts, in.gitRoot, in.workDir),
			RepoRoot:       in.gitRoot,
			SideWidthRatio: in.opts.Display.TreeWidth,
			NoSidePane:     in.opts.Display.NoTree,
			MouseTracking:  !in.opts.Display.NoMouse,
			NoStatusBar:    in.opts.Display.NoStatusBar,
			LogDiffFiles:   in.opts.Commit.LogDiffFiles,
			NoVerify:       in.opts.Commit.NoVerify,
			Signoff:        in.opts.Commit.Signoff,
		}
		setup.start, setup.startNote = openWhereTheWorkIs(in.opts, in.inProgress, setup.start)
	}
	return setup
}

// openWhereTheWorkIs lets startModeFor decide unless a subcommand already named
// the mode.
func openWhereTheWorkIs(opts options, op gitops.InProgress, start tui.Mode) (tui.Mode, string) {
	if start != tui.ModeReview || opts.commitSubcommand || opts.prSubcommand {
		return start, "" // a subcommand named the mode, or commit is already it
	}
	return startModeFor(start, op)
}

// startModeFor moves an unasked-for review start into commit mode while a
// merge, rebase or cherry-pick is unfinished: the conflicts are settled and the
// result committed there, and review mode has nothing to add until then. The
// note says so, because a session that opens somewhere the flags did not ask
// for should explain itself.
func startModeFor(start tui.Mode, op gitops.InProgress) (tui.Mode, string) {
	if !op.Active() {
		return start, ""
	}
	return tui.ModeCommit, op.Label() + ", started in commit mode where it is finished"
}

// fileScope builds the commit-mode file filter from --include, --exclude and
// --only so the Files tab shows the same set of paths the review tree would.
// Paths from git status are relative to the repository root. --only patterns
// match a path exactly or as a "/"-delimited suffix, and are also tried after
// resolving them against the start directory (so "./x.go" or an absolute path
// works from a subdirectory). nil means no filtering.
func fileScope(opts options, gitRoot, workDir string) func(string) bool {
	if len(opts.Include) == 0 && len(opts.Exclude) == 0 && len(opts.Review.Only) == 0 {
		return nil
	}
	prefixes := git.PathFilter(opts.Include, opts.Exclude)
	only := onlyPatterns(opts.Review.Only, gitRoot, workDir)
	return func(path string) bool {
		if !prefixes(path) {
			return false
		}
		if len(only) == 0 {
			return true
		}
		for _, p := range only {
			if path == p || strings.HasSuffix(path, "/"+p) {
				return true
			}
		}
		return false
	}
}

// onlyPatterns normalizes --only values for repository-relative matching: each
// pattern is kept as typed (minus a leading "./") and, when it can be resolved
// against workDir inside gitRoot, also as that root-relative path.
func onlyPatterns(only []string, gitRoot, workDir string) []string {
	out := make([]string, 0, 2*len(only))
	for _, p := range only {
		p = strings.TrimPrefix(filepath.ToSlash(p), "./")
		if p == "" {
			continue
		}
		out = append(out, p)
		if gitRoot == "" {
			continue
		}
		abs := p
		if !filepath.IsAbs(abs) {
			if workDir == "" {
				continue
			}
			abs = filepath.Join(workDir, p)
		}
		if rel, err := filepath.Rel(gitRoot, abs); err == nil && rel != p && !strings.HasPrefix(rel, "..") {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}
