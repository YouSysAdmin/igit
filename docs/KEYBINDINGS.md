# Keybindings

Every key below is a default. `?` inside igit shows the same list for the mode
you are in, and `igit --dump-keys` prints the effective bindings in the file
format described at the end.

Keys are per mode. Review mode reviews a diff and collects annotations, commit
mode stages and commits. `alt+g` switches between them, and the mode bar at the
top row is clickable. A few actions exist in both maps under the same name, the
tables list each mode separately so what you see is what that mode answers to.

Some keys are deliberately left unbound because host terminals take them first,
`ctrl+p` for example is used by agterm and tmux, and `ctrl+g` by zellij.

## Review mode

### Navigation

| keys         | action             | what it does              |
|--------------|--------------------|---------------------------|
| `down` / `j` | `down`             | move cursor down          |
| `k` / `up`   | `up`               | move cursor up            |
| `pgdown`     | `page_down`        | page down                 |
| `pgup`       | `page_up`          | page up                   |
| `ctrl+d`     | `half_page_down`   | half page down            |
| `ctrl+u`     | `half_page_up`     | half page up              |
| `home`       | `home`             | go to top                 |
| `end`        | `end`              | go to bottom              |
| `left`       | `scroll_left`      | scroll left               |
| `right`      | `scroll_right`     | scroll right / focus diff |
| `J`          | `scroll_diff_down` | scroll diff down          |
| `K`          | `scroll_diff_up`   | scroll diff up            |

### File/Hunk

| keys      | action                | what it does                 |
|-----------|-----------------------|------------------------------|
| `n`       | `next_item`           | next file / search match     |
| `N` / `p` | `prev_item`           | prev file / search match     |
| `P`       | `jump_file`           | jump to file                 |
| `]`       | `next_hunk`           | next hunk                    |
| `[`       | `prev_hunk`           | prev hunk                    |
| `e`       | `open_file_in_editor` | open focused file in $EDITOR |

### Pane

| keys  | action        | what it does      |
|-------|---------------|-------------------|
| `tab` | `toggle_pane` | toggle pane focus |
| `h`   | `focus_tree`  | focus tree pane   |
| `l`   | `focus_diff`  | focus diff pane   |

### Search

| keys | action   | what it does   |
|------|----------|----------------|
| `/`  | `search` | search in diff |

### Annotations

| keys          | action               | what it does                                  |
|---------------|----------------------|-----------------------------------------------|
| `a` / `enter` | `confirm`            | annotate line / select file                   |
| `A`           | `annotate_file`      | annotate file                                 |
| `d`           | `delete_annotation`  | delete annotation                             |
| `@`           | `annot_list`         | annotation list                               |
| `ctrl+e`      | `open_editor`        | open annotation in $EDITOR                    |
| `alt+enter`   | `annotation_newline` | line break while typing an annotation         |
| `}`           | `next_annotation`    | next annotation (across files)                |
| `{`           | `prev_annotation`    | previous annotation (across files)            |
| `O`           | `flush_output`       | flush annotations to the output file          |
| `ctrl+s`      | `save_as`            | save annotations to a file (prompts for path) |

### View

| keys    | action                | what it does                  |
|---------|-----------------------|-------------------------------|
| `v`     | `toggle_collapsed`    | toggle collapsed view         |
| `C`     | `toggle_compact`      | toggle compact diff view      |
| `w`     | `toggle_wrap`         | toggle word wrap              |
| `t`     | `toggle_tree`         | toggle tree pane              |
| `L`     | `toggle_line_numbers` | toggle line numbers           |
| `B`     | `toggle_blame`        | toggle blame gutter           |
| `W`     | `toggle_word_diff`    | toggle word-diff highlighting |
| `.`     | `toggle_hunk`         | toggle hunk in collapsed      |
| `u`     | `toggle_untracked`    | untracked files: show or hide |
| `space` | `mark_reviewed`       | mark file as reviewed         |
| `F`     | `filter_unreviewed`   | show unreviewed files         |
| `f`     | `filter`              | filter files                  |
| `T`     | `theme_select`        | theme selector                |
| `i`     | `info`                | show review info popup        |
| `R`     | `reload`              | reload diff from VCS          |

### Stage

| keys | action             | what it does                                     |
|------|--------------------|--------------------------------------------------|
| `s`  | `stage_mark`       | mark hunk / selected lines / file for the commit |
| `S`  | `stage_mark_file`  | mark whole file for the commit                   |
| `V`  | `visual_range`     | toggle line range selection                      |
| `c`  | `commit_with_plan` | stage marked changes and open commit mode        |

### Mode

| keys    | action        | what it does                          |
|---------|---------------|---------------------------------------|
| `alt+g` | `toggle_mode` | switch between review and commit mode |

### Quit

| keys  | action            | what it does                     |
|-------|-------------------|----------------------------------|
| `q`   | `quit`            | quit                             |
| `Q`   | `quit_discarding` | quit, discarding the annotations |
| `?`   | `help`            | show help                        |
| `esc` | `dismiss`         | dismiss / cancel                 |

## Commit mode

### Navigation

| keys         | action                    | what it does                       |
|--------------|---------------------------|------------------------------------|
| `down` / `j` | `commit:down`             | move cursor down                   |
| `k` / `up`   | `commit:up`               | move cursor up                     |
| `pgdown`     | `commit:page_down`        | page down                          |
| `pgup`       | `commit:page_up`          | page up                            |
| `ctrl+d`     | `commit:half_page_down`   | half page down                     |
| `ctrl+u`     | `commit:half_page_up`     | half page up                       |
| `home`       | `commit:home`             | go to top                          |
| `end`        | `commit:end`              | go to bottom                       |
| `left`       | `commit:scroll_left`      | scroll left                        |
| `right`      | `commit:scroll_right`     | scroll right                       |
| `J`          | `commit:scroll_diff_down` | scroll diff down                   |
| `K`          | `commit:scroll_diff_up`   | scroll diff up                     |
| `]`          | `commit:next_hunk`        | next hunk                          |
| `[`          | `commit:prev_hunk`        | prev hunk                          |
| `n`          | `commit:next_item`        | next file / entry in the side pane |
| `N` / `p`    | `commit:prev_item`        | prev file / entry in the side pane |

### Pane

| keys  | action                | what it does      |
|-------|-----------------------|-------------------|
| `tab` | `commit:toggle_pane`  | toggle pane focus |
| `h`   | `commit:focus_tree`   | focus side pane   |
| `l`   | `commit:focus_diff`   | focus diff pane   |
| `1`   | `commit:tab_files`    | files tab         |
| `2`   | `commit:tab_branches` | branches tab      |
| `3`   | `commit:tab_log`      | log tab           |
| `4`   | `commit:tab_stash`    | stash tab         |
| `>`   | `commit:next_tab`     | next tab          |
| `<`   | `commit:prev_tab`     | previous tab      |

### Staging

| keys         | action                       | what it does                                                                                         |
|--------------|------------------------------|------------------------------------------------------------------------------------------------------|
| `enter`      | `commit:confirm`             | select / open                                                                                        |
| `space`      | `commit:stage_toggle`        | stage or unstage the file, the cursor line or the selected lines (a conflict opens the resolve menu) |
| `s`          | `commit:stage_hunk`          | stage or unstage the change block under the cursor (the file in the side pane)                       |
| `a`          | `commit:stage_all`           | stage all changes                                                                                    |
| `u`          | `commit:unstage_all`         | unstage all changes                                                                                  |
| `d`          | `commit:discard_changes`     | discard changes (asks for confirmation)                                                              |
| `I`          | `commit:toggle_staged_view`  | switch between unstaged and staged diff                                                              |
| `shift+down` | `commit:select_extend_down`  | extend line selection down                                                                           |
| `shift+up`   | `commit:select_extend_up`    | extend line selection up                                                                             |
| `V`          | `commit:visual_range`        | start / stop a visual line range                                                                     |
| `v`          | `commit:hunk_mode`           | toggle change-block selection                                                                        |
| `e`          | `commit:open_file_in_editor` | open file in $EDITOR                                                                                 |

### Commit

| keys | action                 | what it does                |
|------|------------------------|-----------------------------|
| `c`  | `commit:commit`        | commit staged changes       |
| `C`  | `commit:commit_editor` | commit with $EDITOR message |
| `A`  | `commit:amend`         | amend last commit           |

### Stash

| keys | action         | what it does |
|------|----------------|--------------|
| `S`  | `commit:stash` | stash menu   |

### Branches

| keys | action                | what it does                                   |
|------|-----------------------|------------------------------------------------|
| `b`  | `commit:create`       | new branch (from the selected one) / new stash |
| `r`  | `commit:rename`       | rename branch                                  |
| `m`  | `commit:merge`        | merge branch into current                      |
| `o`  | `commit:rebase`       | rebase current branch onto the selected one    |
| `g`  | `commit:reset`        | reset current branch to the selected one       |
| `U`  | `commit:set_upstream` | set upstream                                   |

### Sync

| keys | action                     | what it does                                                   |
|------|----------------------------|----------------------------------------------------------------|
| `P`  | `commit:push`              | push                                                           |
| `F`  | `commit:pull`              | pull                                                           |
| `f`  | `commit:fetch`             | fetch                                                          |
| `M`  | `commit:abort_or_continue` | abort or continue the merge, rebase or cherry-pick in progress |

### View

| keys | action                       | what it does                  |
|------|------------------------------|-------------------------------|
| `/`  | `commit:search`              | search in diff                |
| `t`  | `commit:toggle_tree`         | toggle side pane              |
| `w`  | `commit:toggle_wrap`         | toggle word wrap              |
| `L`  | `commit:toggle_line_numbers` | toggle line numbers           |
| `W`  | `commit:toggle_word_diff`    | toggle word-diff highlighting |
| `T`  | `commit:theme_select`        | theme selector                |
| `R`  | `commit:reload`              | refresh status                |

### Mode

| keys    | action               | what it does                          |
|---------|----------------------|---------------------------------------|
| `alt+g` | `commit:toggle_mode` | switch between review and commit mode |

### Quit

| keys  | action           | what it does     |
|-------|------------------|------------------|
| `q`   | `commit:quit`    | quit             |
| `?`   | `commit:help`    | show help        |
| `esc` | `commit:dismiss` | dismiss / cancel |

## Reading the status bar

Both modes end their status bar with the same block: the view-toggle strip and
the help key. One lamp per toggle the mode owns, lit when the mode is on and
muted when it is off.

| lamp       | toggle                             | mode   |
|------------|------------------------------------|--------|
| `▼`        | collapsed diff                     | review |
| `⊂`        | compact diff                       | review |
| `◉`        | file filter                        | review |
| `↩`        | word wrap                          | both   |
| `≋`        | search matches                     | both   |
| `⊟`        | side pane hidden                   | both   |
| `#`        | line numbers                       | both   |
| `b`        | blame gutter                       | review |
| `±`        | word diff                          | both   |
| `✓` / `○` | files reviewed / unreviewed filter | review |
| `∅`        | untracked files hidden             | review |

Commit mode lists only the five it answers to, a lamp for a key that does
nothing in that mode would be misleading. The shared toggles work from either
pane in both modes.

The left side differs because the modes describe different things: review shows
the file, its stats and the hunk or line position, commit shows the branch, its
upstream and a key legend for the focused pane. The legend drops its trailing
items as the window narrows, and never truncates one in half.

## Notes on individual keys

- `space` marks the current file reviewed, in the keybindings file it is written
  as `space`
- `enter` doubles as annotate in the diff pane and select in the tree
- `a` annotates a line, `A` the whole file. Type `hunk` at the start of a comment
  to annotate the whole hunk
- inside the annotation editor `alt+enter` inserts a line break, `enter` saves
  and `ctrl+e` hands the text to `$EDITOR`. Terminals that send `\x1b\r` for
  `shift+enter` get that chord for free, see [CONFIGURATION.md](CONFIGURATION.md)
- `s`, `S`, `V` and `c` in review mode only work on a git working-tree review,
  they cannot stage lines out of a two-ref or pull-request diff
- a conflict is settled at two levels. `space` on the **file** in the list
  offers the whole-file choices: mark resolved as it stands, take our side, take
  theirs. `space` on a **conflict block** in the diff pane offers that one block:
  keep ours, keep theirs, or keep both with ours first. Line staging is off for
  such a file, git refuses to apply a patch to an unmerged path
- the block menu names the branches git wrote next to the markers, so it reads
  `keep ours (HEAD)` and `keep theirs (feature)`. Resolving a block rewrites only
  that block, the rest of the file is left byte for byte
- `e` opens the file in `$EDITOR` when a block needs more than one side, for
  instance to combine the two and add a comment. The file stays unmerged until
  it is staged, so nothing is decided behind your back
- `M` acts on a merge, rebase, cherry-pick or revert left half-finished. The
  status bar names it next to the branch, as in `master · merging`
- a session started in a tree with one of those unfinished opens in commit mode,
  where the conflicts are settled and the result committed, and says so in the
  status bar. `alt+g` still reaches review mode, it is a start, not a lock
- `u` is refused while one of those is in progress. `git reset` drops every
  merge stage and the `MERGE_HEAD` with them, which leaves a tree that can be
  neither resolved nor aborted. Undo one file at a time, or abort with `M`
- `space` on a file staged during a merge brings its conflict back when it was
  one, rather than freezing whatever the resolution left on disk
- `e` opens the file as it is on disk, from either pane in both modes. Review
  mode offers it only while the diff describes the working tree, so it is off
  for a two-ref compare and for a pull or merge request, where the status bar
  says why

## Overriding them

Write `~/.config/igit/keybindings`, or point `--keys` / `IGIT_KEYS` elsewhere.
The format is one directive per line:

```
# review mode is the default target
map <key> <action>
unmap <key>

# a "commit:" prefix targets commit mode
map commit:<key> <action>
unmap commit:<key>
```

Blank lines and `#` comments are ignored, an unknown action name is reported as
a warning and skipped, and the last mapping of a key wins. Defaults stay in
place for everything the file does not mention, so `unmap` is how you free a
key without rebinding it.

Chords are written `leader>second`, for example `map g>g home`. A key that
leads a chord cannot also be bound on its own.

Key names follow what the terminal reports: `ctrl+e`, `alt+g`, `pgdown`, `esc`,
`space`, `enter`, `left`. A few friendlier spellings are accepted and rewritten
to the canonical name: `return`, `escape`, `pageup`, `page_up`, `pagedown`,
`page_down`, `ctrl+enter` and `meta+enter`. Case matters for single characters,
`j` and `J` are different keys.

`igit --dump-keys` writes the current set in exactly this format, so it is the
quickest way to start a custom file:

```sh
igit --dump-keys > ~/.config/igit/keybindings
```
