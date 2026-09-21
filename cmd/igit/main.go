package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/jessevdk/go-flags"
	"github.com/muesli/termenv"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/extcmd"
	"github.com/yousysadmin/igit/internal/forge"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/highlight"
	"github.com/yousysadmin/igit/internal/keymap"
	"github.com/yousysadmin/igit/internal/session"
	"github.com/yousysadmin/igit/internal/theme"
	"github.com/yousysadmin/igit/internal/tui"
	"github.com/yousysadmin/igit/internal/tui/overlay"
	"github.com/yousysadmin/igit/internal/tui/sidepane"
	"github.com/yousysadmin/igit/internal/tui/style"
	"github.com/yousysadmin/igit/internal/tui/worddiff"
	"github.com/yousysadmin/igit/internal/update"
)

var revision = "unknown"

func main() {
	opts, parseErr := parseArgs(os.Args[1:])
	if parseErr != nil {
		flagsErr, isFlagsErr := errors.AsType[*flags.Error](parseErr)
		if isFlagsErr && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}
		if !isFlagsErr {
			fmt.Fprintf(os.Stderr, "error: %v\n", parseErr)
		}
		os.Exit(1)
	}

	// early-exit commands that don't need theme resolution
	if opts.Version {
		info, _ := debug.ReadBuildInfo()
		fmt.Printf("version: %s\n", buildVersion(revision, info))
		os.Exit(0)
	}

	if opts.updateSubcommand {
		if err := runUpdate(opts, revision, os.Stdout, update.Updater{}); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if opts.DumpConfig {
		dumpConfig(os.Args[1:], os.Stdout)
		os.Exit(0)
	}

	if opts.DumpKeys {
		kms := keymap.LoadSetOrDefault(resolveFlagPath(os.Args[1:], "keys", "IGIT_KEYS", defaultKeysPath))
		if err := kms.Dump(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	themesDir := defaultThemesDir()
	cat := theme.NewCatalog(themesDir)
	done, thErr := handleThemes(&opts, cat, os.Stdout, os.Stderr)
	if thErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", thErr)
		os.Exit(1)
	}
	if done {
		os.Exit(0)
	}

	if opts.DumpTheme {
		colors := collectColors(opts)
		th := theme.Theme{Colors: colors, ChromaStyle: opts.ChromaStyle}
		if err := th.Dump(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func buildVersion(rev string, info *debug.BuildInfo) string {
	if rev != "" && rev != "unknown" {
		return rev
	}
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "unknown"
}

func run(opts options) error {
	// force lipgloss to truecolor when colors are enabled. igit's raw-ANSI
	// helpers (style.ansiColor) always emit truecolor, but lipgloss respects
	// the termenv-detected profile, which can downgrade to ANSI256 / ANSI in
	// tmux or terminals where TERM/COLORTERM detection regresses. The mismatch
	// makes lipgloss-rendered colors (pane borders, file tree fg) look wrong
	// while raw-ANSI paths (line prefix wrap, overlay title injection) render
	// correctly. Forcing truecolor unifies the two paths.
	if !opts.Display.NoColors {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}

	store := annot.NewStore()
	hl := highlight.New(opts.ChromaStyle, !opts.Display.NoColors)
	kms := keymap.LoadSetOrDefault(resolveKeysPath(opts))
	km := kms.Review

	programOptions, closeOutput, err := programOptionsFor(opts)
	if err != nil {
		return err
	}
	defer closeOutput()

	// a pull-request session resolves the request first and then reviews
	// merge base..head like any two-ref diff
	pick := terminalPicker(programOptions)
	var prReview tui.PRReviewer
	var prNotes []tui.RemoteNote
	var reviewLabel string
	var prNumber int
	var prHead, prKind, prNoun string
	var prBaseline, startNote string
	if opts.prSubcommand {
		sess, perr := preparePullRequest(context.Background(), prRequest{
			ref: opts.prRef, forge: forge.Kind(opts.Forge), since: opts.Review.Since,
			opts: opts, pickFor: pick, warn: os.Stderr,
		})
		if perr != nil {
			return perr
		}
		// the displayed range may be narrower than the request's own diff, but
		// annotations are anchored and saved against the request either way
		opts.Refs.Base, opts.Refs.Against = cmp.Or(sess.baseline, sess.pr.MergeBase), sess.pr.HeadSHA
		opts.prFullRef = sess.pr.Ref()
		prReview = prReviewer{pr: sess.pr, hub: sess.hub}
		prNotes = sess.notes
		reviewLabel = sess.pr.Label()
		prNumber = sess.pr.Number
		prHead = shortSHA(sess.pr.HeadSHA)
		prKind = sess.pr.ScopeKind()
		prNoun = sess.pr.Noun()
		if sess.baseline != "" {
			prBaseline = shortSHA(sess.baseline)
			reviewLabel += " since " + prBaseline
			startNote = fmt.Sprintf("showing %s since %s, the full request is igit pr %d", sess.pr.Name(), prBaseline, sess.pr.Number)
		}
	}

	setup, err := setupDiffSource(opts)
	if err != nil {
		return err
	}
	source := setup.source
	gitRoot, workDir := setup.gitRoot, setup.workDir
	// read once and hand to both the review config and the mode decision: a
	// half-finished merge widens the review scope and picks the start mode
	var inProgress gitops.InProgress
	if setup.isGit && gitRoot != "" {
		inProgress = gitops.New(gitRoot).InProgress(context.Background())
	}
	untrackedFn := filterUntracked(setup.untrackedFn, opts.Include, opts.Exclude)
	isGit := setup.isGit

	restored, perr := loadSavedAnnotations(opts, savedAnnotationsReq{
		store: store, source: source, gitRoot: gitRoot, workDir: workDir, pr: prNumber, prHead: prHead, prKind: prKind,
		untrackedFn: untrackedFn, untrackedRenamesFn: setup.untrackedRenamesFn, pick: pick("Saved reviews"),
	})
	if perr != nil {
		return perr
	}

	// construct the three style types Resolver first, Renderer from Resolver, SGR is zero-value
	styleColors := optsToStyleColors(opts)
	var res style.Resolver
	if opts.Display.NoColors {
		res = style.PlainResolver()
	} else {
		res = style.NewResolver(styleColors)
	}

	themesDir := defaultThemesDir()
	configPath := resolveFlagPath(os.Args[1:], "config", "IGIT_CONFIG", defaultConfigPath)
	themes := &themeCatalog{
		catalog:    theme.NewCatalog(themesDir),
		configPath: configPath,
	}

	repoLabel := session.RepoLabel(gitRoot, workDir, opts.Review.Only)
	defaultOutputFn, saveAsFn := outputPathFuncs(opts, repoLabel, workDir)
	reviewCfg := tui.ModelConfig{
		DiffSource:           source,
		Store:                store,
		Highlighter:          hl,
		StyleResolver:        res,
		StyleRenderer:        style.NewRenderer(res),
		SGR:                  style.SGR{},
		WordDiffer:           worddiff.New(),
		Editor:               extcmd.Editor{Dir: cmp.Or(workDir, gitRoot)},
		Overlay:              overlay.NewManager(),
		Themes:               themes,
		Blamer:               setup.blamer,
		LoadUntracked:        untrackedFn,
		LoadUntrackedRenames: setup.untrackedRenamesFn,
		Keymap:               km,
		CommitLog:            setup.commitLogger,
		Applicable: tui.Applicable{
			CommitLog: commitLogApplicable(opts, setup.commitLogger),
			Reload:    true,
			Compact:   compactApplicable(source),
			StagePlan: stagePlanApplicable(opts, isGit),
		},
		NoColors:         opts.Display.NoColors,
		MouseTracking:    !opts.Display.NoMouse,
		NoStatusBar:      opts.Display.NoStatusBar,
		NoConfirmDiscard: opts.Review.NoConfirmDiscard,
		NoConfirmReload:  opts.Review.NoConfirmReload,
		NoTree:           opts.Display.NoTree,
		Wrap:             opts.Display.Wrap,
		WrapIndent:       opts.Display.WrapIndent,
		PageOverlap:      opts.Display.PageOverlap,
		Collapsed:        opts.Review.Collapsed,
		Compact:          opts.Review.Compact,
		CompactContext:   opts.Review.CompactContext,
		CrossFileHunks:   opts.Display.CrossFileHunks,
		StartAtChange:    opts.Display.StartAtChange,
		LineNumbers:      opts.Display.LineNumbers,
		ShowBlame:        opts.Review.Blame,
		ShowUntracked:    opts.startupUntracked(),
		WordDiff:         opts.Display.WordDiff,
		ReviewInfo: reviewInfoFromOptions(opts, reviewInfoInputs{
			workDir:  workDir,
			isGit:    isGit,
			label:    reviewLabel,
			baseline: prBaseline,
		}),
		TabWidth:          opts.Display.TabWidth,
		Ref:               opts.ref(),
		Staged:            opts.Review.Staged,
		TreeWidthRatio:    opts.Display.TreeWidth,
		Only:              opts.Review.Only,
		WorkDir:           workDir,
		SourceEditor:      sourceEditorPolicy(opts, workDir),
		ActiveThemeName:   themes.catalog.ActiveName(opts.Theme),
		AnnotationMarker:  opts.Review.AnnotationMarker,
		OutputPath:        opts.Review.Output,
		DefaultOutputPath: defaultOutputFn,
		SaveAsPath:        saveAsFn,
		MergeInProgress:   inProgress.Active(),
		PRReview:          prReview,
		RemoteNotes:       prNotes,
		NewFileTree: func(entries []git.FileEntry) tui.FileTreeComponent {
			return sidepane.NewFileTree(entries)
		},
	}
	modes := resolveModes(modeInputs{opts: opts, keymap: kms.Commit, review: reviewCfg, gitRoot: gitRoot, workDir: workDir, isGit: isGit, inProgress: inProgress})
	app, err := tui.NewApp(tui.AppConfig{
		Review: reviewCfg, Commit: modes.commit, PlanApplier: modes.planner,
		Unavailable: modes.unavailable, StartMode: modes.start, StartNote: cmp.Or(modes.startNote, startNote),
	})
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}

	p := tea.NewProgram(app, programOptions...)
	guard := &shutdownGuard{}
	stop := guard.watch(p)
	defer stop()
	finalModel, runErr := p.Run()
	// capture the signal flag once: a signal can land between reads, so a graceful
	// runErr==nil exit must not observe wasSignaled flipping to true mid-tail.
	signaled := guard.wasSignaled()
	// restore default signal disposition before finalize. saveHistory shells out
	// to git and writes files, so a slow or hung finalize must stay interruptible
	// by a second signal (default disposition terminates) rather than being caught
	// and swallowed by the guard. defer stop() above stays as a panic safety net -
	// stop is idempotent.
	stop()

	// persist annotations: history safety net + optional -o handoff. a
	// signal-driven exit can still surface a TUI error - a PTY hangup returns
	// EIO that races the guard's QuitMsg - so the history safety net must run
	// whenever the model is available and the exit was graceful or signaled.
	if a, ok := finalModel.(tui.App); ok && (runErr == nil || signaled) {
		m := a.Review()
		return finalize(finalizeReq{
			opts:          opts,
			annotations:   m.Store().FormatOutput(),
			files:         m.Store().Files(),
			discarded:     m.Discarded(),
			gitRoot:       gitRoot,
			workDir:       workDir,
			signaled:      signaled,
			sessionOutput: m.OutputPath(),
			repoLabel:     repoLabel,
			prNumber:      prNumber,
			prHead:        prHead,
			prKind:        prKind,
			restored:      restored,
			prNote:        prSubmissionNote(m.PRSubmission(), prNoun),
			prPosted:      m.PRSubmission() != nil,
			stdout:        os.Stdout,
			stderr:        os.Stderr,
		})
	}
	if runErr != nil {
		return fmt.Errorf("TUI error: %w", runErr)
	}
	return nil
}

// programOptionsFor builds the bubbletea program options: alt screen, our own
// signal handling, the TUI output handle (redirected to /dev/tty when stdout is
// not a terminal) and mouse tracking unless disabled. closeOutput releases the
// tty handle and is a no-op when stdout is used directly.
func programOptionsFor(opts options) (programOptions []tea.ProgramOption, closeOutput func(), err error) {
	programOptions = []tea.ProgramOption{tea.WithAltScreen(), tea.WithoutSignalHandler()}
	tuiOut, err := (tuiOutput{
		stdout:     os.Stdout,
		isTerminal: term.IsTerminal,
		openTTY: func() (*os.File, error) {
			// stdin mode and Bubble Tea's default input fallback open a separate
			// read handle. each side owns and closes its handle independently.
			return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		},
	}).open()
	if err != nil {
		return nil, nil, err
	}
	closeOutput = func() {}
	if tuiOut != os.Stdout {
		closeOutput = func() { _ = tuiOut.Close() }
	}
	programOptions = append(programOptions, tea.WithOutput(tuiOut))
	if !opts.Display.NoMouse {
		programOptions = append(programOptions, tea.WithMouseCellMotion())
	}
	return programOptions, closeOutput, nil
}

type tuiOutput struct {
	stdout     *os.File
	isTerminal func(uintptr) bool
	openTTY    func() (*os.File, error)
}

// open keeps Bubble Tea's display traffic out of redirected stdout, which is
// reserved for the final annotation stream. Terminal stdout is returned as-is.
func (r tuiOutput) open() (*os.File, error) {
	if r.isTerminal(r.stdout.Fd()) {
		return r.stdout, nil
	}
	tty, err := r.openTTY()
	if err != nil {
		return nil, fmt.Errorf("igit requires an interactive terminal for the TUI: %w", err)
	}
	return tty, nil
}

// sourceEditorPolicy decides whether the source editor is offered. It opens the
// file as it is on disk, so it only makes sense while the diff describes the
// working tree. A two-ref compare does not, and a pull or merge request is a
// two-ref compare, so both are off: editing the local file there would change
// something the review does not show.
func sourceEditorPolicy(opts options, workDir string) tui.SourceEditorPolicy {
	if workDir == "" || opts.Refs.Against != "" {
		return tui.SourceEditorPolicy{}
	}
	return tui.SourceEditorPolicy{
		Available: true,
		Root:      workDir,
		// a worktree review reloads the file after an edit so the diff
		// matches what was just saved
		ReloadAfterCleanExit: !opts.Review.Staged && opts.ref() == "",
	}
}

// resolveKeysPath returns the effective keybindings file path, falling back
// to defaultKeysPath() when --keys was not set.
func resolveKeysPath(opts options) string {
	if opts.Keys == "" {
		return defaultKeysPath()
	}
	return opts.Keys
}

// commitLogApplicable returns true when the unified info popup can include a
// commit-log section: a VCS-backed log source must be present and the mode
// must be ref-based (no stdin, staged, all-files, or empty ref). Computed
// once in the composition root so the Model does not re-derive from CLI
// flags. --only is fine when combined with a ref in a real repo. the empty
// ref check excludes the standalone --only / FileReader case where the
// commitLogger is nil anyway.
func commitLogApplicable(opts options, cl git.CommitLogger) bool {
	if cl == nil {
		return false
	}
	if opts.Review.Staged {
		return false
	}
	return opts.ref() != ""
}

// compactApplicable returns true when the current invocation can shrink the
// VCS diff via the compact toggle. false for stdin (no VCS), all-files (no
// hunks to contextualize), and standalone file review via FileReader (pure
// context-only source with no diff to contextualize). All other shapes -
// *Git, with or without Fallback / Include / Exclude wrappers -
// qualify because the wrapper chain delegates FileDiff straight through to
// a VCS that honors contextLines.
func compactApplicable(r tui.DiffSource) bool {
	if _, ok := r.(*git.FileReader); ok {
		return false
	}
	return true
}
