# Configuration

igit reads its settings from three places. Every option has a command-line
flag, almost every one also has an environment variable and a config-file key.

## Precedence

```
command-line flag  >  config file  >  environment variable  >  built-in default
```

A subcommand names its own mode and outranks every default, and a tree with an
unfinished merge or rebase opens in commit mode whatever `mode` says, because
that is where such work is finished.

The config file outranks the environment, which is the opposite of what many
tools do. A value written in `~/.config/igit/config` therefore wins over an
exported `IGIT_*` variable, and only a flag on the command line overrides it.
Comment the key out in the config file when you want the environment to decide.

## Files

| path                         | holds                                               | override with                       |
|------------------------------|-----------------------------------------------------|-------------------------------------|
| `~/.config/igit/config`      | the options below, in INI form                      | `--config`, `IGIT_CONFIG`           |
| `~/.config/igit/keybindings` | key overrides, see [KEYBINDINGS.md](KEYBINDINGS.md) | `--keys`, `IGIT_KEYS`               |
| `~/.config/igit/themes/`     | installed theme files                               | `--theme`, `IGIT_THEME`             |
| `~/.config/igit/history/`    | per-scope review auto-saves                         | `--history-dir`, `IGIT_HISTORY_DIR` |

`--config`, `--keys`, `--dump-config`, `--dump-keys`, `--dump-theme`, the theme
management flags and `--version` have no config-file key, they only make sense
on the command line. `IGIT_CONFIG` and `IGIT_KEYS` are read before the config
file is parsed, so they always apply.

## Environment variables

Every variable below is named after its flag. List options take one value per
variable, separated by the delimiter named in the description.

### Application Options

| variable                | flag                 | config key         | default                        | description                                                                   |
|-------------------------|----------------------|--------------------|--------------------------------|-------------------------------------------------------------------------------|
| `IGIT_MODE`             | `--mode`             | `mode`             | `review` (`review` / `commit`) | start in review or commit mode                                                |
| `IGIT_FORGE`            | `--forge`            | `forge`            | - (`github` / `gitlab`)        | code host of the pull or merge request (default: from the origin remote)      |
| `IGIT_INCLUDE`          | `-I`, `--include`    | `include`          | -                              | include only files matching prefix (may be repeated), values separated by `,` |
| `IGIT_EXCLUDE`          | `-X`, `--exclude`    | `exclude`          | -                              | exclude files matching prefix (may be repeated), values separated by `,`      |
| `IGIT_THEME`            | `--theme`            | `theme`            | -                              | load theme from themes directory                                              |
| `IGIT_AUTO_THEME_DARK`  | `--auto-theme-dark`  | `auto-theme-dark`  | `default`                      | theme to use for dark terminal backgrounds when --theme=auto                  |
| `IGIT_AUTO_THEME_LIGHT` | `--auto-theme-light` | `auto-theme-light` | `basic`                        | theme to use for light terminal backgrounds when --theme=auto                 |
| `IGIT_CHROMA_STYLE`     | `--chroma-style`     | `chroma-style`     | `catppuccin-macchiato`         | chroma style for syntax highlighting                                          |
| `IGIT_KEYS`             | `--keys`             | -                  | -                              | path to keybindings file                                                      |
| `IGIT_CONFIG`           | `--config`           | -                  | -                              | path to config file                                                           |
| -                       | `--check`            | -                  | -                              | with `igit update`: report whether a newer release exists instead of installing it |

### Display Options

| variable                | flag                 | config key         | default | description                                                                                                                                                                              |
|-------------------------|----------------------|--------------------|---------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `IGIT_TREE_WIDTH`       | `--tree-width`       | `tree-width`       | -       | side pane width in tenths of the window, 1-10 (default 2 in review mode, 3 in commit mode)                                                                                               |
| `IGIT_TAB_WIDTH`        | `--tab-width`        | `tab-width`        | `4`     | number of spaces per tab character                                                                                                                                                       |
| `IGIT_NO_COLORS`        | `--no-colors`        | `no-colors`        | -       | disable all colors including syntax highlighting                                                                                                                                         |
| `IGIT_NO_STATUS_BAR`    | `--no-status-bar`    | `no-status-bar`    | -       | hide the status bar                                                                                                                                                                      |
| `IGIT_NO_MOUSE`         | `--no-mouse`         | `no-mouse`         | -       | disable mouse support (scroll wheel, click)                                                                                                                                              |
| `IGIT_NO_TREE`          | `--no-tree`          | `no-tree`          | -       | hide the file tree pane                                                                                                                                                                  |
| `IGIT_WRAP`             | `--wrap`             | `wrap`             | -       | enable line wrapping in diff view                                                                                                                                                        |
| `IGIT_WRAP_INDENT`      | `--wrap-indent`      | `wrap-indent`      | `0`     | indent wrap continuation rows by N columns so they hang under the first row's content (helps when reviewing markdown lists where unindented continuation can be misread as a new bullet) |
| `IGIT_PAGE_OVERLAP`     | `--page-overlap`     | `page-overlap`     | `0`     | keep N lines from the previous screen when paging the diff                                                                                                                               |
| `IGIT_CROSS_FILE_HUNKS` | `--cross-file-hunks` | `cross-file-hunks` | -       | allow [ and ] to jump across file boundaries                                                                                                                                             |
| `IGIT_START_AT_CHANGE`  | `--start-at-change`  | `start-at-change`  | -       | position the cursor on the first changed line                                                                                                                                            |
| `IGIT_LINE_NUMBERS`     | `--line-numbers`     | `line-numbers`     | -       | show line numbers in diff gutter                                                                                                                                                         |
| `IGIT_WORD_DIFF`        | `--word-diff`        | `word-diff`        | -       | highlight intra-line word-level changes in paired add/remove lines                                                                                                                       |

### Review Options

| variable                              | flag                               | config key                       | default | description                                                                                                           |
|---------------------------------------|------------------------------------|----------------------------------|---------|-----------------------------------------------------------------------------------------------------------------------|
| `IGIT_STAGED`                         | `--staged`                         | `staged`                         | -       | show staged changes                                                                                                   |
| `IGIT_TRACKED_ONLY`                   | `--tracked-only`                   | `tracked-only`                   | -       | start with untracked files hidden (u toggles them back)                                                               |
| `IGIT_COLLAPSED`                      | `--collapsed`                      | `collapsed`                      | -       | start in collapsed diff mode                                                                                          |
| `IGIT_COMPACT`                        | `--compact`                        | `compact`                        | -       | start in compact diff mode (small context around changes)                                                             |
| `IGIT_COMPACT_CONTEXT`                | `--compact-context`                | `compact-context`                | `5`     | number of context lines around changes when in compact mode                                                           |
| `IGIT_BLAME`                          | `--blame`                          | `blame`                          | -       | show blame gutter                                                                                                     |
| `IGIT_ANNOTATION_MARKER`              | `--annotation-marker`              | `annotation-marker`              | `💬`    | prefix shown before annotation lines                                                                                  |
| `IGIT_NO_CONFIRM_DISCARD_ANNOTATIONS` | `--no-confirm-discard-annotations` | `no-confirm-discard-annotations` | -       | skip the confirmation prompt when quitting with Q, which discards the annotations                                     |
| `IGIT_NO_CONFIRM_RELOAD`              | `--no-confirm-reload`              | `no-confirm-reload`              | -       | skip confirmation prompt when dropping annotations on reload with R                                                   |
| `IGIT_OUTPUT`                         | `-o`, `--output`                   | -                                | -       | write annotations to file instead of stdout                                                                           |
| `IGIT_OUTPUT_DIR`                     | `--output-dir`                     | `output-dir`                     | -       | directory for timestamped annotation files written on exit                                                            |
| `IGIT_HISTORY_DIR`                    | `--history-dir`                    | `history-dir`                    | -       | directory for review history auto-saves                                                                               |
| `IGIT_HISTORY_MAX`                    | `--history-max`                    | `history-max`                    | `20`    | keep at most N saved reviews per repository, one per working tree, ref range or pull request (0 disables the history) |

### Commit Options

| variable              | flag               | config key       | default | description                                                                                                                                            |
|-----------------------|--------------------|------------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
| `IGIT_LOG_DIFF_FILES` | `--log-diff-files` | `log-diff-files` | `10`    | files shown in the combined diff of the highlighted Log or Stash entry, enter opens the full list (0 shows every file, which is slow on large commits) |
| `IGIT_NO_VERIFY`      | `--no-verify`      | `no-verify`      | -       | skip pre-commit and commit-msg hooks when committing                                                                                                   |
| `IGIT_SIGNOFF`        | `--signoff`        | `signoff`        | -       | add a Signed-off-by trailer to commits                                                                                                                 |

### Color Options

| variable                    | flag                     | config key             | default   | description                                                    |
|-----------------------------|--------------------------|------------------------|-----------|----------------------------------------------------------------|
| `IGIT_COLOR_ACCENT`         | `--color-accent`         | `color-accent`         | `#D5895F` | active pane borders and directory names                        |
| `IGIT_COLOR_BORDER`         | `--color-border`         | `color-border`         | `#585858` | inactive pane borders                                          |
| `IGIT_COLOR_NORMAL`         | `--color-normal`         | `color-normal`         | `#d0d0d0` | file entries and context lines                                 |
| `IGIT_COLOR_MUTED`          | `--color-muted`          | `color-muted`          | `#585858` | line numbers and status bar                                    |
| `IGIT_COLOR_SELECTED_FG`    | `--color-selected-fg`    | `color-selected-fg`    | `#ffffaf` | selected file text color                                       |
| `IGIT_COLOR_SELECTED_BG`    | `--color-selected-bg`    | `color-selected-bg`    | `#D5895F` | selected file background color                                 |
| `IGIT_COLOR_ANNOTATION`     | `--color-annotation`     | `color-annotation`     | `#ffd700` | annotation text and markers                                    |
| `IGIT_COLOR_CURSOR_FG`      | `--color-cursor-fg`      | `color-cursor-fg`      | `#bbbb44` | diff cursor indicator color                                    |
| `IGIT_COLOR_CURSOR_BG`      | `--color-cursor-bg`      | `color-cursor-bg`      | -         | diff cursor indicator background                               |
| `IGIT_COLOR_ADD_FG`         | `--color-add-fg`         | `color-add-fg`         | `#87d787` | added line text color                                          |
| `IGIT_COLOR_ADD_BG`         | `--color-add-bg`         | `color-add-bg`         | `#123800` | added line background color                                    |
| `IGIT_COLOR_REMOVE_FG`      | `--color-remove-fg`      | `color-remove-fg`      | `#ff8787` | removed line text color                                        |
| `IGIT_COLOR_REMOVE_BG`      | `--color-remove-bg`      | `color-remove-bg`      | `#4D1100` | removed line background color                                  |
| `IGIT_COLOR_WORD_ADD_BG`    | `--color-word-add-bg`    | `color-word-add-bg`    | -         | intra-line word-diff add background (auto-derived if empty)    |
| `IGIT_COLOR_WORD_REMOVE_BG` | `--color-word-remove-bg` | `color-word-remove-bg` | -         | intra-line word-diff remove background (auto-derived if empty) |
| `IGIT_COLOR_MODIFY_FG`      | `--color-modify-fg`      | `color-modify-fg`      | `#f5c542` | modified line text color (collapsed mode)                      |
| `IGIT_COLOR_MODIFY_BG`      | `--color-modify-bg`      | `color-modify-bg`      | `#3D2E00` | modified line background color (collapsed mode)                |
| `IGIT_COLOR_TREE_BG`        | `--color-tree-bg`        | `color-tree-bg`        | -         | file tree pane background                                      |
| `IGIT_COLOR_DIFF_BG`        | `--color-diff-bg`        | `color-diff-bg`        | -         | diff pane background                                           |
| `IGIT_COLOR_STATUS_FG`      | `--color-status-fg`      | `color-status-fg`      | `#202020` | status bar foreground                                          |
| `IGIT_COLOR_STATUS_BG`      | `--color-status-bg`      | `color-status-bg`      | `#C5794F` | status bar background                                          |
| `IGIT_COLOR_SEARCH_FG`      | `--color-search-fg`      | `color-search-fg`      | `#1a1a1a` | search match foreground                                        |
| `IGIT_COLOR_SEARCH_BG`      | `--color-search-bg`      | `color-search-bg`      | `#4a4a00` | search match background                                        |

## Variables igit reads but does not own

| variable                       | read by                                  | effect                                                                                            |
|--------------------------------|------------------------------------------|---------------------------------------------------------------------------------------------------|
| `EDITOR`                       | annotation editor, commit message editor | the command to run, tried first                                                                   |
| `VISUAL`                       | same                                     | tried when `EDITOR` is unset or empty                                                             |
| `TERM`, `TERM_PROGRAM`, `TMUX` | terminal background detection            | decide how the background color is queried, and whether the tmux path is used, for `--theme=auto` |

Neither is set by igit. When both `EDITOR` and `VISUAL` are empty it falls back
to `vi`. Quoting is POSIX-style, so `EDITOR="code --wait"` and
`EDITOR='sh -c \'vim "$@"\' --'` both work.

igit sets `GIT_TERMINAL_PROMPT=0` on the git commands it runs itself, so a
credential prompt can never block the TUI. Interactive work (editor commits,
push, pull, fetch, GPG) hands the terminal over instead and keeps prompts
enabled.

## Example config file

[`config.example`](config.example) is the complete reference: every option with
its default, commented out. Copy it and uncomment what you need.

```sh
cp docs/config.example ~/.config/igit/config
```

`igit --dump-config` prints the same thing, so the file can be regenerated at any
time and reflects the options of the binary you are running:

```sh
igit --dump-config > ~/.config/igit/config
```

Section headers match the option groups in `--help` and the tables above. A key
stays inactive while its leading comment marker is there, uncommenting it makes
it win over the matching environment variable.
