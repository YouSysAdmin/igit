package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jessevdk/go-flags"
)

// options is the full command line. Flags shared by both modes live at the top
// level ("Application Options" in --help and the config file). the nested
// groups hold the display settings both modes honor, the review-only flags
// and the commit-only flags, each printed as its own --help section and stored
// under its own [section] in the config file.
type options struct {
	Refs struct {
		Base    string `positional-arg-name:"base" description:"git ref to diff against (default: uncommitted changes)"`
		Against string `positional-arg-name:"against" description:"second git ref for two-ref diff (e.g. igit main feature)"`
	} `positional-args:"yes"`

	commitSubcommand bool   // true when "igit commit" was invoked
	prSubcommand     bool   // true when "igit pr [ref]" was invoked
	prRef            string // what gh resolves for igit pr: number, URL or branch, empty = current branch
	prFullRef        string // full request range of a request review, the annotation scope
	updateSubcommand bool   // true when "igit update" was invoked

	Mode           string   `long:"mode" ini-name:"mode" env:"IGIT_MODE" choice:"review" choice:"commit" default:"review" description:"start in review or commit mode"`
	Forge          string   `long:"forge" ini-name:"forge" env:"IGIT_FORGE" choice:"github" choice:"gitlab" description:"code host of the pull or merge request (default: from the origin remote)"`
	Include        []string `long:"include" short:"I" ini-name:"include" env:"IGIT_INCLUDE" env-delim:"," description:"include only files matching prefix (may be repeated)"`
	Exclude        []string `long:"exclude" short:"X" ini-name:"exclude" env:"IGIT_EXCLUDE" env-delim:"," description:"exclude files matching prefix (may be repeated)"`
	Theme          string   `long:"theme" ini-name:"theme" env:"IGIT_THEME" description:"load theme from themes directory"`
	AutoThemeDark  string   `long:"auto-theme-dark" ini-name:"auto-theme-dark" env:"IGIT_AUTO_THEME_DARK" default:"default" description:"theme to use for dark terminal backgrounds when --theme=auto"`
	AutoThemeLight string   `long:"auto-theme-light" ini-name:"auto-theme-light" env:"IGIT_AUTO_THEME_LIGHT" default:"basic" description:"theme to use for light terminal backgrounds when --theme=auto"`
	ChromaStyle    string   `long:"chroma-style" ini-name:"chroma-style" env:"IGIT_CHROMA_STYLE" default:"catppuccin-macchiato" description:"chroma style for syntax highlighting"`
	DumpTheme      bool     `long:"dump-theme" no-ini:"true" description:"print currently resolved colors as theme file and exit"`
	ListThemes     bool     `long:"list-themes" no-ini:"true" description:"print available theme names and exit"`
	InitThemes     bool     `long:"init-themes" no-ini:"true" description:"write bundled theme files to themes dir and exit"`
	InitAllThemes  bool     `long:"init-all-themes" no-ini:"true" description:"write all gallery themes (bundled + community) to themes dir and exit"`
	InstallTheme   []string `long:"install-theme" no-ini:"true" description:"install theme(s) from gallery or local file path and exit"`
	Keys           string   `long:"keys" env:"IGIT_KEYS" no-ini:"true" description:"path to keybindings file"`
	DumpKeys       bool     `long:"dump-keys" no-ini:"true" description:"print effective keybindings to stdout and exit"`
	Config         string   `long:"config" env:"IGIT_CONFIG" no-ini:"true" description:"path to config file"`
	DumpConfig     bool     `long:"dump-config" no-ini:"true" description:"print default config to stdout and exit"`
	Version        bool     `short:"V" long:"version" no-ini:"true" description:"show version info"`
	Check          bool     `long:"check" no-ini:"true" description:"with igit update: report whether a newer release exists instead of installing it"`

	Display displayOptions `group:"Display Options"`
	Review  reviewOptions  `group:"Review Options"`
	Commit  commitOptions  `group:"Commit Options"`

	Colors struct {
		Accent       string `long:"color-accent"      ini-name:"color-accent"      env:"IGIT_COLOR_ACCENT"      default:"#D5895F" description:"active pane borders and directory names"`
		Border       string `long:"color-border"      ini-name:"color-border"      env:"IGIT_COLOR_BORDER"      default:"#585858" description:"inactive pane borders"`
		Normal       string `long:"color-normal"      ini-name:"color-normal"      env:"IGIT_COLOR_NORMAL"      default:"#d0d0d0" description:"file entries and context lines"`
		Muted        string `long:"color-muted"       ini-name:"color-muted"       env:"IGIT_COLOR_MUTED"       default:"#585858" description:"line numbers and status bar"`
		SelectedFg   string `long:"color-selected-fg" ini-name:"color-selected-fg" env:"IGIT_COLOR_SELECTED_FG" default:"#ffffaf" description:"selected file text color"`
		SelectedBg   string `long:"color-selected-bg" ini-name:"color-selected-bg" env:"IGIT_COLOR_SELECTED_BG" default:"#D5895F" description:"selected file background color"`
		Annotation   string `long:"color-annotation"  ini-name:"color-annotation"  env:"IGIT_COLOR_ANNOTATION"  default:"#ffd700" description:"annotation text and markers"`
		CursorFg     string `long:"color-cursor-fg"   ini-name:"color-cursor-fg"   env:"IGIT_COLOR_CURSOR_FG"   default:"#bbbb44" description:"diff cursor indicator color"`
		CursorBg     string `long:"color-cursor-bg"   ini-name:"color-cursor-bg"   env:"IGIT_COLOR_CURSOR_BG"   description:"diff cursor indicator background"`
		AddFg        string `long:"color-add-fg"      ini-name:"color-add-fg"      env:"IGIT_COLOR_ADD_FG"      default:"#87d787" description:"added line text color"`
		AddBg        string `long:"color-add-bg"      ini-name:"color-add-bg"      env:"IGIT_COLOR_ADD_BG"      default:"#123800" description:"added line background color"`
		RemoveFg     string `long:"color-remove-fg"   ini-name:"color-remove-fg"   env:"IGIT_COLOR_REMOVE_FG"   default:"#ff8787" description:"removed line text color"`
		RemoveBg     string `long:"color-remove-bg"   ini-name:"color-remove-bg"   env:"IGIT_COLOR_REMOVE_BG"   default:"#4D1100" description:"removed line background color"`
		WordAddBg    string `long:"color-word-add-bg"    ini-name:"color-word-add-bg"    env:"IGIT_COLOR_WORD_ADD_BG"    description:"intra-line word-diff add background (auto-derived if empty)"`
		WordRemoveBg string `long:"color-word-remove-bg" ini-name:"color-word-remove-bg" env:"IGIT_COLOR_WORD_REMOVE_BG" description:"intra-line word-diff remove background (auto-derived if empty)"`
		ModifyFg     string `long:"color-modify-fg"      ini-name:"color-modify-fg"      env:"IGIT_COLOR_MODIFY_FG"      default:"#f5c542" description:"modified line text color (collapsed mode)"`
		ModifyBg     string `long:"color-modify-bg"   ini-name:"color-modify-bg"   env:"IGIT_COLOR_MODIFY_BG"   default:"#3D2E00" description:"modified line background color (collapsed mode)"`
		TreeBg       string `long:"color-tree-bg"     ini-name:"color-tree-bg"     env:"IGIT_COLOR_TREE_BG"     description:"file tree pane background"`
		DiffBg       string `long:"color-diff-bg"     ini-name:"color-diff-bg"     env:"IGIT_COLOR_DIFF_BG"     description:"diff pane background"`
		StatusFg     string `long:"color-status-fg"   ini-name:"color-status-fg"   env:"IGIT_COLOR_STATUS_FG"   default:"#202020" description:"status bar foreground"`
		StatusBg     string `long:"color-status-bg"   ini-name:"color-status-bg"   env:"IGIT_COLOR_STATUS_BG"   default:"#C5794F" description:"status bar background"`
		SearchFg     string `long:"color-search-fg"   ini-name:"color-search-fg"   env:"IGIT_COLOR_SEARCH_FG"   default:"#1a1a1a" description:"search match foreground"`
		SearchBg     string `long:"color-search-bg"   ini-name:"color-search-bg"   env:"IGIT_COLOR_SEARCH_BG"   default:"#4a4a00" description:"search match background"`
	} `group:"Color Options"`
}

// displayOptions are the layout and rendering settings honored by both modes.
type displayOptions struct {
	TreeWidth      int  `long:"tree-width" ini-name:"tree-width" env:"IGIT_TREE_WIDTH" description:"side pane width in tenths of the window, 1-10 (default 2 in review mode, 3 in commit mode)"`
	TabWidth       int  `long:"tab-width" ini-name:"tab-width" env:"IGIT_TAB_WIDTH" default:"4" description:"number of spaces per tab character"`
	NoColors       bool `long:"no-colors" ini-name:"no-colors" env:"IGIT_NO_COLORS" description:"disable all colors including syntax highlighting"`
	NoStatusBar    bool `long:"no-status-bar" ini-name:"no-status-bar" env:"IGIT_NO_STATUS_BAR" description:"hide the status bar"`
	NoMouse        bool `long:"no-mouse" ini-name:"no-mouse" env:"IGIT_NO_MOUSE" description:"disable mouse support (scroll wheel, click)"`
	NoTree         bool `long:"no-tree" ini-name:"no-tree" env:"IGIT_NO_TREE" description:"hide the file tree pane"`
	Wrap           bool `long:"wrap" ini-name:"wrap" env:"IGIT_WRAP" description:"enable line wrapping in diff view"`
	WrapIndent     int  `long:"wrap-indent" ini-name:"wrap-indent" env:"IGIT_WRAP_INDENT" default:"0" description:"indent wrap continuation rows by N columns so they hang under the first row's content (helps when reviewing markdown lists where unindented continuation can be misread as a new bullet)"`
	PageOverlap    int  `long:"page-overlap" ini-name:"page-overlap" env:"IGIT_PAGE_OVERLAP" default:"0" description:"keep N lines from the previous screen when paging the diff"`
	CrossFileHunks bool `long:"cross-file-hunks" ini-name:"cross-file-hunks" env:"IGIT_CROSS_FILE_HUNKS" description:"allow [ and ] to jump across file boundaries"`
	StartAtChange  bool `long:"start-at-change" ini-name:"start-at-change" env:"IGIT_START_AT_CHANGE" description:"position the cursor on the first changed line"`
	LineNumbers    bool `long:"line-numbers" ini-name:"line-numbers" env:"IGIT_LINE_NUMBERS" description:"show line numbers in diff gutter"`
	WordDiff       bool `long:"word-diff" ini-name:"word-diff" env:"IGIT_WORD_DIFF" description:"highlight intra-line word-level changes in paired add/remove lines"`
}

// reviewOptions select what review mode shows and how annotations leave the session.
type reviewOptions struct {
	Staged           bool     `long:"staged" ini-name:"staged" env:"IGIT_STAGED" description:"show staged changes"`
	TrackedOnly      bool     `long:"tracked-only" ini-name:"tracked-only" env:"IGIT_TRACKED_ONLY" description:"start with untracked files hidden (u toggles them back)"`
	Only             []string `long:"only" short:"F" no-ini:"true" description:"show only these files (may be repeated)"`
	Annotations      string   `long:"annotations" no-ini:"true" description:"preload annotations from a markdown file written by -o (round-trip)"`
	Resume           bool     `long:"resume" no-ini:"true" description:"continue the last saved review of this repository from the history, with a picker when several match"`
	Since            string   `long:"since" no-ini:"true" description:"with igit pr: review only what changed since this revision, or since your own last submitted review with --since=review (use refs/heads/review for a branch of that name)"`
	Collapsed        bool     `long:"collapsed" ini-name:"collapsed" env:"IGIT_COLLAPSED" description:"start in collapsed diff mode"`
	Compact          bool     `long:"compact" ini-name:"compact" env:"IGIT_COMPACT" description:"start in compact diff mode (small context around changes)"`
	CompactContext   int      `long:"compact-context" ini-name:"compact-context" env:"IGIT_COMPACT_CONTEXT" default:"5" description:"number of context lines around changes when in compact mode"`
	Blame            bool     `long:"blame" ini-name:"blame" env:"IGIT_BLAME" description:"show blame gutter"`
	AnnotationMarker string   `long:"annotation-marker" ini-name:"annotation-marker" env:"IGIT_ANNOTATION_MARKER" default:"💬" description:"prefix shown before annotation lines"`
	NoConfirmDiscard bool     `long:"no-confirm-discard-annotations" ini-name:"no-confirm-discard-annotations" env:"IGIT_NO_CONFIRM_DISCARD_ANNOTATIONS" description:"skip the confirmation prompt when quitting with Q, which discards the annotations (commit mode always confirms discarding worktree changes)"`
	NoConfirmReload  bool     `long:"no-confirm-reload" ini-name:"no-confirm-reload" env:"IGIT_NO_CONFIRM_RELOAD" description:"skip confirmation prompt when dropping annotations on reload with R"`
	Output           string   `long:"output" short:"o" env:"IGIT_OUTPUT" no-ini:"true" description:"write annotations to file instead of stdout"`
	OutputDir        string   `long:"output-dir" ini-name:"output-dir" env:"IGIT_OUTPUT_DIR" description:"directory for timestamped annotation files written on exit"`
	HistoryDir       string   `long:"history-dir" ini-name:"history-dir" env:"IGIT_HISTORY_DIR" description:"directory for review history auto-saves"`
	HistoryMax       int      `long:"history-max" ini-name:"history-max" env:"IGIT_HISTORY_MAX" default:"20" description:"keep at most N saved reviews per repository, one per working tree, ref range or pull request (0 disables the history)"`
}

// commitOptions are commit mode's own settings.
type commitOptions struct {
	LogDiffFiles int  `long:"log-diff-files" ini-name:"log-diff-files" env:"IGIT_LOG_DIFF_FILES" default:"10" description:"files shown in the combined diff of the highlighted Log or Stash entry, enter opens the full list (0 shows every file, which is slow on large commits)"`
	NoVerify     bool `long:"no-verify" ini-name:"no-verify" env:"IGIT_NO_VERIFY" description:"skip pre-commit and commit-msg hooks when committing"`
	Signoff      bool `long:"signoff" ini-name:"signoff" env:"IGIT_SIGNOFF" description:"add a Signed-off-by trailer to commits"`
}

// ref returns the combined ref string from positional args.
// two refs are joined with ".." to form a range (e.g. "main..feature").
func (o options) ref() string {
	if o.Refs.Against != "" {
		return o.Refs.Base + ".." + o.Refs.Against
	}
	return o.Refs.Base
}

// scopeRef is the range annotations are anchored and saved against. It is
// ref() everywhere except an incremental request review, where the displayed
// range is narrower than the request's own diff and would otherwise drop every
// annotation outside it.
func (o options) scopeRef() string {
	return cmp.Or(o.prFullRef, o.ref())
}

// startupUntracked reports whether untracked files are part of the review.
// They are by default, unless --tracked-only asks otherwise or the session
// compares two refs, where the working tree plays no part.
func (o options) startupUntracked() bool {
	if o.Review.TrackedOnly {
		return false
	}
	return o.Refs.Against == "" && !strings.Contains(o.Refs.Base, "..")
}

// parseArgs parses CLI arguments with config file support.
// config file is loaded first, then CLI args override.
// precedence: CLI flags > env vars > config file > built-in defaults.
func parseArgs(args []string) (options, error) {
	var opts options
	p := flags.NewParser(&opts, flags.Default)
	p.Usage = "[OPTIONS]"
	p.LongDescription = "Review a diff with inline annotations, or stage and commit changes. Run `igit commit` (or --mode commit) to start in commit mode, and `igit pr [number|url|branch]` (or `igit mr`) to review a GitHub pull request or a GitLab merge request and post the annotations as a review on quit (needs the gh or glab CLI, picked from the origin remote or --forge). `igit update` replaces the binary with the newest GitHub release, and `igit update --check` only reports whether one is available. A subcommand names its own mode and outranks --mode, so a default mode set in the config file or an alias never breaks it. A ref literally named commit, pr, mr or update can be reviewed with `igit -- commit`. Display options apply to both modes, review and commit options to their mode only. The config file uses the same option names under matching [section] headers (see --dump-config)."

	// determine config path from args before full parsing
	configPath := resolveFlagPath(args, "config", "IGIT_CONFIG", defaultConfigPath)

	// load config file before parsing CLI args (CLI overrides config)
	iniParser := flags.NewIniParser(p)
	loadConfigFile(iniParser, configPath)

	if _, err := p.ParseArgs(args); err != nil {
		return options{}, fmt.Errorf("parse args: %w", err)
	}
	opts = detectCommitSubcommand(opts, args)
	opts = detectPRSubcommand(opts, args)
	opts = detectUpdateSubcommand(opts, args)

	if err := validateOptions(opts); err != nil {
		return options{}, err
	}

	return opts, nil
}

// validateOptions rejects flag combinations that have no sensible meaning
// together and values outside their range.
func validateOptions(opts options) error {
	if err := validatePRFlags(opts); err != nil {
		return err
	}

	if opts.Check && !opts.updateSubcommand {
		return errors.New("--check is only valid with igit update")
	}

	if opts.Review.Staged && (opts.Refs.Against != "" || strings.Contains(opts.Refs.Base, "..")) {
		return errors.New("--staged cannot be used with two-ref diff")
	}

	if opts.Review.Resume && opts.Review.Annotations != "" {
		return errors.New("--resume and --annotations are mutually exclusive")
	}
	if opts.Review.HistoryMax < 0 {
		return errors.New("--history-max must be >= 0")
	}

	if len(opts.Include) > 0 && len(opts.Review.Only) > 0 {
		return errors.New("--include cannot be used with --only")
	}

	if opts.Review.CompactContext <= 0 {
		return errors.New("--compact-context must be >= 1")
	}

	if opts.Commit.LogDiffFiles < 0 {
		return errors.New("--log-diff-files must be >= 0")
	}

	if opts.Display.TreeWidth < 0 || opts.Display.TreeWidth > 10 {
		return errors.New("--tree-width must be between 1 and 10")
	}

	if strings.ContainsAny(opts.Review.AnnotationMarker, "\n\r\t") {
		return errors.New("--annotation-marker cannot contain control characters")
	}
	return nil
}

// dumpConfig writes the current config with defaults to the given writer.
func dumpConfig(args []string, w io.Writer) {
	var opts options
	p := flags.NewParser(&opts, flags.Default)
	iniParser := flags.NewIniParser(p)
	configPath := resolveFlagPath(args, "config", "IGIT_CONFIG", defaultConfigPath)
	loadConfigFile(iniParser, configPath)
	_, _ = p.ParseArgs(args)
	iniParser.Write(w, flags.IniIncludeDefaults|flags.IniCommentDefaults|flags.IniIncludeComments)
}

// loadConfigFile attempts to parse a config file, logging a warning on parse errors.
// silently ignores missing files or empty paths.
func loadConfigFile(iniParser *flags.IniParser, configPath string) {
	if configPath == "" {
		return
	}
	err := iniParser.ParseFile(configPath)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return
	}
	if _, ok := errors.AsType[*os.PathError](err); ok {
		return // file access error (permission denied, etc.)
	}
	fmt.Fprintf(os.Stderr, "warning: config %s: %v\n", configPath, err)
}

// resolveFlagPath determines a file path from CLI args, env var, or default location.
// it checks args for --flag value and --flag=value forms, falls back to envVar, then defaultFn.
func resolveFlagPath(args []string, flag, envVar string, defaultFn func() string) string {
	longFlag := "--" + flag
	for i, arg := range args {
		if arg == longFlag && i+1 < len(args) {
			return args[i+1]
		}
		if after, ok := strings.CutPrefix(arg, longFlag+"="); ok {
			return after
		}
	}
	if p := os.Getenv(envVar); p != "" {
		return p
	}
	return defaultFn()
}

// defaultConfigPath returns ~/.config/igit/config.
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "igit", "config")
}

// defaultKeysPath returns ~/.config/igit/keybindings.
func defaultKeysPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "igit", "keybindings")
}
