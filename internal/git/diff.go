package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ChangeType represents the type of change for a diff line.
type ChangeType string

const (
	ChangeAdd     ChangeType = "+"
	ChangeRemove  ChangeType = "-"
	ChangeContext ChangeType = " "
	ChangeDivider ChangeType = "~" // marks a skipped unchanged region (leading, between-hunk, or trailing)

	// fullContextSentinel is the numeric threshold that callers use to request
	// full-file diff context. contextLines <= 0 or >= fullContextSentinel causes
	// unifiedContextArg to return the full-file argument form.
	fullContextSentinel = 1000000

	// fullFileContext is the -U value treated as "give me the full file". use
	// unifiedContextArg at call sites to choose
	// between full-file and small-context based on the caller's contextLines value.
	fullFileContext = "-U1000000"

	// MaxLineLength is the maximum line length (in bytes) that scanners will accept.
	// used by parseUnifiedDiff, readReaderAsContext, and parseBlame.
	MaxLineLength = 1024 * 1024

	// BinaryPlaceholder is the content used for binary file placeholders.
	// parseUnifiedDiff returns this when git reports "Binary files ... differ".
	BinaryPlaceholder = "(binary file)"
)

// DiffLine holds parsed line info from a diff.
type DiffLine struct {
	OldNum        int        // line number in old version (0 for additions)
	NewNum        int        // line number in new version (0 for removals)
	Content       string     // line content without the +/- prefix, for ChangeDivider rows it is a human-readable gap label - never pattern-match it, dispatch on ChangeType
	ChangeType    ChangeType // changeAdd, ChangeRemove, ChangeContext, or ChangeDivider
	IsBinary      bool       // true when this line is a binary file placeholder
	IsPlaceholder bool       // true for non-content placeholders (broken symlink, non-regular file, too-long lines)
	IsFileHeader  bool       // divider naming the file whose diff follows, in a multi-file diff
}

// ChangeStatus represents the change type of a file in a diff.
type ChangeStatus string

const (
	FileAdded     ChangeStatus = "A"
	FileModified  ChangeStatus = "M"
	FileDeleted   ChangeStatus = "D"
	FileRenamed   ChangeStatus = "R"
	FileUntracked ChangeStatus = "?"
	// FileUnmerged is a path with unresolved merge conflicts. git reports it
	// alongside a second record for the same path, ParseNameStatus folds the
	// pair into this one status.
	FileUnmerged ChangeStatus = "U"
)

// FileEntry represents a file with its change status from a diff.
type FileEntry struct {
	Path    string       // file path relative to repo root
	OldPath string       // rename origin, empty for non-renames
	Status  ChangeStatus // file change status, empty for standalone file sources
}

// FileEntryPaths extracts just the paths from a slice of FileEntry.
func FileEntryPaths(entries []FileEntry) []string {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	return paths
}

// FileDiffRequest carries the inputs FileDiff needs to render one file's diff.
// OldPath is the rename origin (empty for non-renames). only the Git renderer consumes it.
type FileDiffRequest struct {
	Ref          string
	Path         string // new/current path
	OldPath      string // rename origin, empty when not a rename
	Staged       bool
	ContextLines int
}

// CountChanges tallies ChangeAdd and ChangeRemove lines in a DiffLine slice.
// ChangeContext and ChangeDivider are excluded. new ChangeType values would
// also fall through, which is the safe default for a line-stat counter.
func CountChanges(lines []DiffLine) (adds, removes int) {
	for _, dl := range lines {
		switch dl.ChangeType {
		case ChangeAdd:
			adds++
		case ChangeRemove:
			removes++
		case ChangeContext, ChangeDivider:
		}
	}
	return adds, removes
}

// MaxCommits is the hard cap on the number of commits returned by any CommitLogger
// implementation. Callers should treat a result of exactly MaxCommits entries as
// potentially truncated and surface that to the user (e.g. via overlay.InfoSpec.Truncated).
const MaxCommits = 500

// CommitInfo holds metadata and message fields for a single commit in a ref range.
// Author, Subject, and Body are pre-sanitized by SanitizeCommitText so overlay
// renderers can treat them as literal text without a second sanitization pass.
type CommitInfo struct {
	Hash    string    // full commit hash
	Author  string    // "Name <email>"
	Date    time.Time // committer date (RFC3339-parsed)
	Subject string    // first line of the commit message
	Body    string    // remainder of the message with trailing blank lines trimmed (may be empty)
}

// CommitLogger is an optional capability interface implemented by the diff
// sources (Git) that can enumerate commits in a ref range. It is deliberately
// separate from DiffSource so consumers type-assert for the capability and gracefully
// fall back when unavailable (e.g. FileReader, DirectoryReader).
//
// The ref argument follows igit's combined-ref convention produced by
// options.ref(): "" means no range is selected, "X" is the single ref form,
// and "X..Y" is the explicit range form. Each implementation translates the
// string to its native log syntax.
type CommitLogger interface {
	CommitLog(ref string) ([]CommitInfo, error)
}

// commitLogFormat is the git-log --format template used by (*Git).CommitLog.
// Fields inside a record are separated by ASCII US (\x1f). records are
// NUL-separated via -z. Subject and body are joined by a newline inside the
// final field so the parser's SplitN naturally absorbs any control bytes
// (including \x1f) that a crafted commit message might embed - splitCommitDesc
// then separates subject and body on the first newline.
const commitLogFormat = "%H%x1f%an <%ae>%x1f%cI%x1f%s%n%b"

// ansiCSIRe matches complete ANSI CSI escape sequences (ESC [ ... final-byte).
// Used by SanitizeCommitText to neutralize ANSI injection via crafted commit messages.
//
//nolint:gocritic // explicit ASCII 0x20..0x2F range for CSI intermediate bytes
var ansiCSIRe = regexp.MustCompile("\x1b\\[[0-9;?]*[\x20-\x2f]*[a-zA-Z~]")

// SanitizeCommitText makes untrusted repository text safe to render verbatim.
// It strips ANSI CSI sequences and stray ESC bytes, C0 controls other than TAB
// and LF, the field delimiters US/RS/NUL, C1 code points (U+0080-U+009F, read
// as 8-bit CSI/OSC by some terminals) and invalid UTF-8. Printable runes, TAB
// and LF pass through.
func SanitizeCommitText(s string) string {
	if !hasUnsafeContent(s) {
		return s
	}
	if strings.ContainsRune(s, 0x1b) {
		s = ansiCSIRe.ReplaceAllString(s, "")
	}
	if !hasUnsafeContent(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// invalid UTF-8 byte - drop it so raw 0x9b/0x9d (and other
			// stray bytes) cannot reach the terminal.
			i++
			continue
		}
		if isUnsafeRune(r) {
			i += size
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// hasUnsafeContent is a fast-path check used by SanitizeCommitText to skip the
// full rune scan when s contains no ESC byte, no unsafe rune, and no invalid
// UTF-8 byte.
func hasUnsafeContent(s string) bool {
	if strings.ContainsRune(s, 0x1b) {
		return true
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return true
		}
		if isUnsafeRune(r) {
			return true
		}
		i += size
	}
	return false
}

// isUnsafeRune reports whether r should be dropped from commit metadata before
// rendering. See SanitizeCommitText for the full rationale. briefly: TAB, LF,
// and printable runes are kept, everything else in the C0/DEL/C1 ranges
// (including CR) is dropped.
func isUnsafeRune(r rune) bool {
	switch {
	case r == 0x09, r == 0x0a:
		return false
	case r < 0x20:
		return true
	case r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	default:
		return false
	}
}

// splitCommitDesc splits a commit description value into subject and body
// on the first newline. A single leading blank line (the conventional
// subject/body separator) is stripped, and trailing blank lines in the body
// are trimmed, matching git's %s/%b split.
func splitCommitDesc(desc string) (subject, body string) {
	subject, rest, found := strings.Cut(desc, "\n")
	if !found {
		return desc, ""
	}
	rest = strings.TrimPrefix(rest, "\n")
	return subject, strings.TrimRight(rest, "\n")
}

// Git is the read side of one working tree: changed files, full-file diff
// views, blame and the commit log. It runs every command through the shared
// Runner, so a failure here carries the same *Error a write in internal/gitops
// produces.
type Git struct {
	*Runner
}

// NewGit creates a Git diff source rooted at the given working directory.
func NewGit(workDir string) *Git {
	return &Git{Runner: NewRunner(workDir)}
}

// CommitLog returns commits reachable in the given ref range, newest first.
//
// The ref argument is interpreted as follows:
//   - ""      -> returns (nil, nil). there is no range to inspect
//   - "X"     -> commits in "X..HEAD"
//   - "X..Y"  -> passed through unchanged
//
// The result is capped at MaxCommits entries, so a result of exactly that
// length should be reported as truncated. Author, Subject and Body are passed
// through SanitizeCommitText.
func (g *Git) CommitLog(ref string) ([]CommitInfo, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	args := []string{
		"log", "--no-color", "-z",
		"--format=" + commitLogFormat,
		"-n", strconv.Itoa(MaxCommits),
		g.commitLogRange(ref),
	}
	out, err := g.runGit(args...)
	if err != nil {
		return nil, fmt.Errorf("commit log: %w", err)
	}
	return g.parseCommitLog(out), nil
}

// commitLogRange translates a combined ref string to git's log range syntax.
// Single ref "X" becomes "X..HEAD". "X..Y" passes through.
func (g *Git) commitLogRange(ref string) string {
	if strings.Contains(ref, "..") {
		return ref
	}
	return ref + "..HEAD"
}

// parseCommitLog parses the raw output of "git log -z --format=<commitLogFormat>"
// into a slice of CommitInfo entries. Records are NUL-separated. within a record
// fields are ASCII-US-separated (hash, author, date, desc) and desc holds subject
// and body joined by a newline. The slice is capped at MaxCommits entries.
func (g *Git) parseCommitLog(raw string) []CommitInfo {
	raw = strings.TrimRight(raw, "\x00")
	if raw == "" {
		return nil
	}
	records := strings.Split(raw, "\x00")
	commits := make([]CommitInfo, 0, len(records))
	for _, record := range records {
		// leading newline can appear between NUL-terminated records after a body
		record = strings.TrimLeft(record, "\n")
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x1f", 4)
		if len(fields) < 4 {
			continue
		}
		subject, body := splitCommitDesc(fields[3])
		ci := CommitInfo{
			Hash:    fields[0],
			Author:  SanitizeCommitText(fields[1]),
			Subject: SanitizeCommitText(subject),
			Body:    SanitizeCommitText(body),
		}
		if t, err := time.Parse(time.RFC3339, fields[2]); err == nil {
			ci.Date = t
		}
		commits = append(commits, ci)
		if len(commits) >= MaxCommits {
			break
		}
	}
	return commits
}

// UntrackedFiles returns untracked files (not in .gitignore) using git ls-files --others --exclude-standard.
func (g *Git) UntrackedFiles() ([]string, error) {
	out, err := g.runGit("ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for entry := range strings.SplitSeq(out, "\x00") {
		if entry != "" {
			files = append(files, entry)
		}
	}
	return files, nil
}

// ChangedFiles returns a list of files changed relative to the given ref with their change status.
// If ref is empty, it shows uncommitted changes. If staged is true, shows only staged changes.
// Uses -z for NUL-terminated output to handle filenames with special characters.
func (g *Git) ChangedFiles(ref string, staged bool) ([]FileEntry, error) {
	args := g.diffArgs(ref, staged)
	// force rename detection (-M) so OldPath is populated regardless of the user's
	// diff.renames config. FileDiff relies on OldPath to pair the rename
	args = append(args, "--name-status", "-M", "-z")

	out, err := g.runGit(args...)
	if err != nil {
		return nil, fmt.Errorf("get changed files: %w", err)
	}

	return g.parseNameStatusEntries(out), nil
}

// fileExists reports whether path names something on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// bareUnmergedNotice reports whether git only announced an unmerged path and
// produced no diff for it.
func bareUnmergedNotice(out string) bool {
	return strings.HasPrefix(out, "* Unmerged path") && !strings.Contains(out, "\n@@")
}

// wholeFileDiff renders the working-tree file as one addition. Returns an empty
// diff when the path is gone from the working tree.
func (g *Git) wholeFileDiff(path string) (string, error) {
	if !fileExists(filepath.Join(g.workDir, path)) {
		return "", nil
	}
	// --no-index implies --exit-code, so a non-empty diff exits 1
	out, err := g.runGitAllowExit(1, "diff", "--no-color", "--no-ext-diff", "--no-index", "--", os.DevNull, path)
	return out, err
}

// parseNameStatusEntries parses NUL-separated `git diff --name-status -z` output into
// FileEntry values. For renames/copies (R<score>, C<score>) it consumes two paths -
// the first is the rename origin (OldPath), the second is the new/current name - and
// normalizes the status to a single letter (R100 -> R).
func (g *Git) parseNameStatusEntries(out string) []FileEntry {
	var entries []FileEntry
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i < len(fields); {
		rawStatus := fields[i]
		if rawStatus == "" {
			i++
			continue
		}
		i++
		if i >= len(fields) {
			break
		}
		path := fields[i]
		i++
		var oldPath string
		if rawStatus[0] == 'R' || rawStatus[0] == 'C' {
			if i < len(fields) {
				oldPath = path
				path = fields[i]
				i++
			}
		}
		if len(rawStatus) > 1 {
			rawStatus = rawStatus[:1]
		}
		entries = append(entries, FileEntry{Path: path, OldPath: oldPath, Status: ChangeStatus(rawStatus)})
	}
	return foldUnmerged(entries)
}

// foldUnmerged collapses the repeated records git emits for an unmerged path.
// `git diff --name-status` reports such a path twice, once as U and once as the
// ordinary change, which would otherwise put the file in the tree twice. The
// first record keeps its place in the list and carries FileUnmerged.
func foldUnmerged(entries []FileEntry) []FileEntry {
	at := make(map[string]int, len(entries))
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		i, seen := at[e.Path]
		if !seen {
			at[e.Path] = len(out)
			out = append(out, e)
			continue
		}
		if e.Status == FileUnmerged || out[i].Status == FileUnmerged {
			out[i].Status = FileUnmerged
		}
	}
	return out
}

// errNoIndex signals that the repo has no index file yet (a fresh repo with no
// commits). UntrackedRenames treats it as "no renames possible" rather than an error.
var errNoIndex = errors.New("git index not found")

// UntrackedRenames detects working-tree renames whose new side is still untracked.
// A plain `mv old new` (no `git mv`, no staging) leaves `old` as an unstaged deletion
// and `new` untracked, so `git diff -M` never pairs them. This pairs them off a
// throwaway index: it copies the repo index, marks the given untracked paths
// intent-to-add (-N) against that copy, and runs `git diff -M --name-status`, which
// then reports the pairs as renames. The real index and working tree are never
// modified. It returns one FileEntry{Status: FileRenamed, Path: new, OldPath: old}
// per detected rename. genuinely new untracked files (no rename origin) are omitted.
func (g *Git) UntrackedRenames(untracked []string) ([]FileEntry, error) {
	if len(untracked) == 0 {
		return nil, nil
	}
	indexPath, cleanup, err := g.tempIndexWithIntentToAdd(untracked)
	if err != nil {
		// no index (fresh repo with no commits) means nothing is tracked, so no
		// deletion exists to pair an untracked file against - no renames possible.
		// only suppress that specific case. any other error (e.g. a missing TMPDIR
		// failing temp creation) must propagate so the caller warns.
		if errors.Is(err, errNoIndex) {
			return nil, nil
		}
		return nil, err
	}
	defer cleanup()

	out, err := g.runGitWithEnv(g.renameIndexEnv(indexPath),
		"diff", "--no-color", "--no-ext-diff", "--name-status", "-M", "-z")
	if err != nil {
		return nil, fmt.Errorf("detect untracked renames: %w", err)
	}

	untrackedSet := make(map[string]bool, len(untracked))
	for _, u := range untracked {
		untrackedSet[u] = true
	}
	var renames []FileEntry
	for _, e := range g.parseNameStatusEntries(out) {
		if e.Status == FileRenamed && untrackedSet[e.Path] {
			renames = append(renames, e)
		}
	}
	return renames, nil
}

// tempIndexWithIntentToAdd copies the repo index to a throwaway file and marks the
// given untracked paths intent-to-add (-N) against the copy. Callers run git with
// GIT_INDEX_FILE set to the returned path so `git diff -M` pairs working-tree renames
// whose new side is still untracked. cleanup removes the temp file. the real index
// and working tree are never modified.
func (g *Git) tempIndexWithIntentToAdd(paths []string) (indexPath string, cleanup func(), err error) {
	realIndex, err := g.runGit("rev-parse", "--git-path", "index")
	if err != nil {
		return "", nil, fmt.Errorf("resolve git index path: %w", err)
	}
	realIndex = strings.TrimSpace(realIndex)
	if !filepath.IsAbs(realIndex) {
		realIndex = filepath.Join(g.workDir, realIndex)
	}
	src, err := os.ReadFile(realIndex) //nolint:gosec // path resolved from git rev-parse, not user input
	if err != nil {
		// a fresh repo with no commits has no index yet. signal that distinctly so
		// the caller can treat it as "no renames possible" rather than a real error.
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, errNoIndex
		}
		return "", nil, fmt.Errorf("read git index: %w", err)
	}

	tmp, err := os.CreateTemp("", "igit-index-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp index: %w", err)
	}
	// git resolves GIT_INDEX_FILE relative to cmd.Dir (g.workDir), but cleanup
	// removes via the process cwd - make the path absolute so both agree even
	// when TMPDIR is relative.
	tmpPath, err := filepath.Abs(tmp.Name())
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("resolve temp index path: %w", err)
	}
	cleanup = func() { _ = os.Remove(tmpPath) }
	if _, err = tmp.Write(src); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", nil, fmt.Errorf("write temp index: %w", err)
	}
	if err = tmp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close temp index: %w", err)
	}

	addArgs := append([]string{"add", "-N", "--"}, paths...)
	if _, err = g.runGitWithEnv(g.renameIndexEnv(tmpPath), addArgs...); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("intent-to-add untracked: %w", err)
	}
	return tmpPath, cleanup, nil
}

// renameIndexEnv builds the extra environment for git commands in the
// untracked-rename path: GIT_INDEX_FILE points at the throwaway index. Literal
// pathspec handling comes from GitEnv, which every git call goes through.
func (g *Git) renameIndexEnv(indexPath string) []string {
	return []string{"GIT_INDEX_FILE=" + indexPath}
}

// FileDiff returns the diff view for a single file.
// The result is a sequence of DiffLine entries representing unchanged, added, and removed lines
// interleaved at their correct positions.
// For binary files, it returns a single placeholder line with size delta information.
// contextLines controls surrounding context: 0 or >= fullContextSentinel requests full-file
// context. positive values below the sentinel request that many lines on each side of a hunk.
func (g *Git) FileDiff(req FileDiffRequest) ([]DiffLine, error) {
	if g.isUntrackedRename(req) {
		return g.untrackedRenameDiff(req)
	}

	args := g.diffArgs(req.Ref, req.Staged)
	args = append(args, unifiedContextArg(req.ContextLines))
	args = append(args, g.pathArgs(req)...)

	out, err := g.runGit(args...)
	if err != nil {
		return nil, fmt.Errorf("get file diff for %s: %w", req.Path, err)
	}
	// a modify/delete conflict has no blob on our side to diff against, so
	// --ours answers with the notice alone. The decision there is whether to
	// keep the file at all, which needs its content, not a line diff
	if bareUnmergedNotice(out) {
		if whole, wholeErr := g.wholeFileDiff(req.Path); wholeErr == nil {
			out = whole
		}
	}

	// trailing divider is only meaningful in compact mode - full-file mode always
	// reaches EOF, so probing the old-file size would be a wasted subprocess.
	total := 0
	if req.ContextLines > 0 && req.ContextLines < fullContextSentinel {
		total = g.totalOldLines(req)
	}
	lines, err := parseUnifiedDiff(out, total)
	if err != nil {
		return nil, err
	}

	// enrich binary placeholder with size delta from git diff --stat
	if len(lines) == 1 && lines[0].IsBinary {
		if desc := g.binarySizeDesc(req); desc != "" {
			lines[0].Content = desc
		}
	}

	return lines, nil
}

// pathArgs builds the pathspec suffix for a file diff. For a rename (OldPath set
// and distinct from Path) it returns "-M -- <old> <new>" so git pairs the rename
// into a minimal diff instead of rendering the file as brand-new, otherwise it
// returns "-- <path>".
func (g *Git) pathArgs(req FileDiffRequest) []string {
	if req.OldPath != "" && req.OldPath != req.Path {
		return []string{"-M", "--", req.OldPath, req.Path}
	}
	return []string{"--", req.Path}
}

// isUntrackedRename reports whether req describes a working-tree rename whose new side
// is still untracked. In unstaged working-tree mode (no ref, not staged) a rename
// always has an untracked new path that git's normal diff cannot see, so a
// throwaway-index pass is required. OldPath is only populated in this mode by
// UntrackedRenames, so the condition uniquely identifies that case.
func (g *Git) isUntrackedRename(req FileDiffRequest) bool {
	return req.Ref == "" && !req.Staged && req.OldPath != "" && req.OldPath != req.Path
}

// untrackedRenameDiff renders the diff for a rename whose new side is untracked by
// running git against a throwaway index that intent-to-adds the new path (see
// tempIndexWithIntentToAdd). Without it, `git diff -M -- old new` reports only the
// deletion of old because the untracked new path is invisible to git.
func (g *Git) untrackedRenameDiff(req FileDiffRequest) ([]DiffLine, error) {
	indexPath, cleanup, err := g.tempIndexWithIntentToAdd([]string{req.Path})
	if err != nil {
		return nil, err
	}
	defer cleanup()

	args := g.diffArgs(req.Ref, req.Staged)
	args = append(args, unifiedContextArg(req.ContextLines), "-M", "--", req.OldPath, req.Path)
	out, err := g.runGitWithEnv(g.renameIndexEnv(indexPath), args...)
	if err != nil {
		return nil, fmt.Errorf("get file diff for %s: %w", req.Path, err)
	}

	total := 0
	if req.ContextLines > 0 && req.ContextLines < fullContextSentinel {
		total = g.totalOldLines(req)
	}
	return parseUnifiedDiff(out, total)
}

// totalOldLines returns the line count of the pre-change version of the file,
// which parseUnifiedDiff needs for the trailing divider. 0 means unknown (new
// file, bad ref) and the divider is skipped. The old side is HEAD for a staged
// diff, the index for a worktree diff, the left operand of a range, otherwise
// the ref itself. For a rename it is read from req.OldPath.
func (g *Git) totalOldLines(req FileDiffRequest) int {
	oldRef := req.Ref
	if left, _, ok := strings.Cut(req.Ref, "..."); ok {
		oldRef = left
	}
	if left, _, ok := strings.Cut(oldRef, ".."); ok {
		oldRef = left
	}
	if oldRef == "" && req.Staged {
		oldRef = "HEAD"
	}
	file := req.Path
	if req.OldPath != "" {
		file = req.OldPath
	}
	// `git show :path` (empty oldRef) shows the index version of the file
	out, err := g.runGit("show", oldRef+":"+file)
	if err != nil {
		return 0
	}
	return countLines(out)
}

// unifiedContextArg returns the -U argument for git diff
// given the caller's requested context size. A non-positive contextLines or one
// at or above fullContextSentinel returns the full-file arg. any other value
// returns -U<contextLines>.
func unifiedContextArg(contextLines int) string {
	if contextLines <= 0 || contextLines >= fullContextSentinel {
		return fullFileContext
	}
	return fmt.Sprintf("-U%d", contextLines)
}

// diffArgs builds the base git diff arguments for the given ref and staged flag.
func (g *Git) diffArgs(ref string, staged bool) []string {
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if staged {
		args = append(args, "--cached")
	}
	if ref != "" {
		args = append(args, ref)
	}
	if ref == "" && !staged {
		// an unmerged path has no plain worktree diff: git answers with a
		// combined diff no unified parser can read. --ours asks for the
		// worktree against our side instead, which is an ordinary diff showing
		// the conflict markers. It changes nothing for every other path, so it
		// is set once here rather than threaded through every call site.
		args = append(args, "--ours")
	}
	return args
}

// runGit executes a git command in the working directory and returns its output.
// The read paths are driven from tea.Cmds that drop stale results by sequence
// rather than canceling, so there is no caller context to thread here yet.
func (g *Git) runGit(args ...string) (string, error) {
	return g.Run(context.Background(), RunOpts{}, args...)
}

// runGitAllowExit runs git and treats one exit code as success, for the
// commands that report a result through their status (`diff --no-index`).
func (g *Git) runGitAllowExit(okCode int, args ...string) (string, error) {
	return g.Run(context.Background(), RunOpts{OkExitCodes: []int{okCode}}, args...)
}

// runGitWithEnv runs git with extra environment entries (e.g. GIT_INDEX_FILE),
// used by the throwaway-index rename detection path.
func (g *Git) runGitWithEnv(extraEnv []string, args ...string) (string, error) {
	return g.Run(context.Background(), RunOpts{ExtraEnv: extraEnv}, args...)
}

// binarySizeDesc runs git diff --stat for a binary file and returns a human-readable
// description like "(new binary file, 2.0 KB)" or "(binary file: 1.0 KB -> 2.0 KB)".
// Returns empty string if stat info is unavailable.
func (g *Git) binarySizeDesc(req FileDiffRequest) string {
	args := g.diffArgs(req.Ref, req.Staged)
	args = append(args, "--stat", "--summary")
	args = append(args, g.pathArgs(req)...)

	out, err := g.runGit(args...)
	if err != nil {
		return ""
	}

	oldSize, newSize, ok := g.parseBinaryStat(out)
	if !ok {
		return ""
	}

	return g.formatBinaryDesc(g.parseBinaryChangeKind(out), oldSize, newSize)
}

type binaryChangeKind int

const (
	binaryChangeModified binaryChangeKind = iota
	binaryChangeAdded
	binaryChangeDeleted
)

// binaryStatRe matches a git diff --stat line ending with "Bin 1234 -> 5678 bytes".
// The entire pattern ("Bin", "->", "bytes") assumes English locale. non-English git
// may localize any of these tokens, causing a graceful fallback to the header-based
// placeholder from parseUnifiedDiff (e.g. "(new binary file)" without size info).
var binaryStatRe = regexp.MustCompile(`^\s*.*\|\s+Bin (\d+) -> (\d+) bytes$`)

var (
	binaryCreateSummaryRe = regexp.MustCompile(`^\s*create mode \d+\s+`)
	binaryDeleteSummaryRe = regexp.MustCompile(`^\s*delete mode \d+\s+`)
)

// parseBinaryStat extracts old and new sizes from git diff --stat output.
// Returns (oldBytes, newBytes, ok).
func (g *Git) parseBinaryStat(statOutput string) (int64, int64, bool) {
	scanner := bufio.NewScanner(strings.NewReader(statOutput))
	for scanner.Scan() {
		m := binaryStatRe.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}

		oldSize, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		newSize, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		return oldSize, newSize, true
	}

	return 0, 0, false
}

func (g *Git) parseBinaryChangeKind(summaryOutput string) binaryChangeKind {
	scanner := bufio.NewScanner(strings.NewReader(summaryOutput))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case binaryCreateSummaryRe.MatchString(line):
			return binaryChangeAdded
		case binaryDeleteSummaryRe.MatchString(line):
			return binaryChangeDeleted
		}
	}

	return binaryChangeModified
}

// formatBinaryDesc builds a human-readable binary file description from old/new byte sizes.
func (g *Git) formatBinaryDesc(kind binaryChangeKind, oldSize, newSize int64) string {
	switch kind {
	case binaryChangeAdded:
		return fmt.Sprintf("(new binary file, %s)", g.formatSize(newSize))
	case binaryChangeDeleted:
		return fmt.Sprintf("(deleted binary file, %s)", g.formatSize(oldSize))
	default:
		return fmt.Sprintf("(binary file: %s → %s)", g.formatSize(oldSize), g.formatSize(newSize))
	}
}

// formatSize formats a byte count as a human-readable string.
func (g *Git) formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// hunkHeaderRe matches unified diff hunk headers like @@ -1,5 +1,7 @@.
// Lengths are optional per git's spec (omitted means length 1) and are captured
// so the parser can compute the old-side end of each hunk.
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// countLines returns the number of lines in s, counting a final non-newline-terminated
// line as one additional line. Empty input returns 0. Used by totalOldLines to
// translate file contents into a line count for the trailing divider.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// appendGapDivider appends an "N lines" gap divider to lines when gap is positive.
// Used for leading, between-hunks, and trailing dividers - same format, different
// source of the gap count. Returns lines unchanged when gap <= 0 (nothing to show).
func appendGapDivider(lines []DiffLine, gap int) []DiffLine {
	switch {
	case gap == 1:
		return append(lines, DiffLine{ChangeType: ChangeDivider, Content: "⋯ 1 line ⋯"})
	case gap > 1:
		return append(lines, DiffLine{ChangeType: ChangeDivider, Content: fmt.Sprintf("⋯ %d lines ⋯", gap)})
	}
	return lines
}

// binaryFilesRe matches git's "Binary files ... differ" line for binary diffs.
// Assumes English locale. non-English git may localize this message.
var binaryFilesRe = regexp.MustCompile(`^Binary files .+ and .+ differ$`)

// parseUnifiedDiff parses unified diff output into a slice of DiffLine entries.
// it handles the diff header, hunk headers, and content lines.
// for binary diffs ("Binary files ... differ"), it returns a single placeholder DiffLine.
// intended for single-file diffs. multi-file diffs are not fully supported.
//
// totalOldLines is the total line count of the pre-change file, used to emit a
// trailing gap divider after the last hunk when it does not reach EOF.
// Pass 0 when unknown (context-only sources, tests, or any case where the caller
// cannot cheaply determine the old file's size) - trailing divider is then skipped.
func parseUnifiedDiff(raw string, totalOldLines int) ([]DiffLine, error) {
	var lines []DiffLine
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), MaxLineLength)

	// skip diff header lines (---, +++, diff --git, index, etc.)
	inHeader := true
	var oldNum, newNum int
	// prevOldEnd = next untouched old-side line. Initialized to 1 so the first hunk's
	// leading divider uses the same `oldStart - prevOldEnd` formula as between-hunks gaps.
	prevOldEnd := 1
	// sawHunk tracks whether any hunk header was parsed - used as the guard for the
	// trailing divider so that insertion-at-start hunks (@@ -0,0 ...) don't collide
	// with the prevOldEnd==1 initialization sentinel.
	var sawHunk bool
	var isNewFile, isDeletedFile bool

	for scanner.Scan() {
		line := scanner.Text()

		if inHeader {
			switch {
			case strings.HasPrefix(line, "new file mode"):
				isNewFile = true
				continue
			case strings.HasPrefix(line, "deleted file mode"):
				isDeletedFile = true
				continue
			case binaryFilesRe.MatchString(line):
				content := BinaryPlaceholder
				switch {
				case isNewFile:
					content = "(new binary file)"
				case isDeletedFile:
					content = "(deleted binary file)"
				}
				return []DiffLine{{OldNum: 1, NewNum: 1, Content: content, ChangeType: ChangeContext, IsBinary: true}}, nil
			case !hunkHeaderRe.MatchString(line):
				continue
			}
			inHeader = false
		}

		// parse hunk header
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			oldStart, errOld := strconv.Atoi(m[1])
			newStart, errNew := strconv.Atoi(m[3])
			if errOld != nil || errNew != nil {
				return nil, fmt.Errorf("parse hunk header %q: old=%w new=%w", line, errOld, errNew)
			}
			// Atoi("") returns 0 with error. regex guarantees m[2] is digits when non-empty.
			// Both the omitted-length case (git spec: implicit 1) and the literal `,0` (insertion-only)
			// end up at oldLen=0 here, and max(oldLen,1) below resolves both to the same advance.
			oldLen, _ := strconv.Atoi(m[2])

			// emit divider representing unchanged lines BEFORE this hunk.
			// Leading divider (first hunk) uses prevOldEnd=1 initialization. between-hunks use
			// prevOldEnd from prior iteration. Gap uses hunk-header metadata not oldNum, so
			// insertion-only hunks (@@ -K,0 ...) compute correctly. oldNum stays put on `+` lines.
			lines = appendGapDivider(lines, oldStart-prevOldEnd)
			sawHunk = true
			// prevOldEnd = line number AFTER the current hunk on the old side. Normal hunks
			// (oldLen>0) cover [oldStart, oldStart+oldLen). Insertion-only hunks (oldLen==0,
			// e.g. @@ -K,0 ...) insert between old lines K and K+1 - handled by max(oldLen,1).
			prevOldEnd = oldStart + max(oldLen, 1)

			oldNum = oldStart
			newNum = newStart
			continue
		}

		// no-newline marker
		if strings.HasPrefix(line, `\ No newline at end of file`) {
			continue
		}

		if line == "" {
			// empty context line (happens for blank lines in source)
			lines = append(lines, DiffLine{OldNum: oldNum, NewNum: newNum, Content: "", ChangeType: ChangeContext})
			oldNum++
			newNum++
			continue
		}

		prefix := line[0]
		content := line[1:]

		switch prefix {
		case '+':
			lines = append(lines, DiffLine{OldNum: 0, NewNum: newNum, Content: content, ChangeType: ChangeAdd})
			newNum++
		case '-':
			lines = append(lines, DiffLine{OldNum: oldNum, NewNum: 0, Content: content, ChangeType: ChangeRemove})
			oldNum++
		case ' ':
			lines = append(lines, DiffLine{OldNum: oldNum, NewNum: newNum, Content: content, ChangeType: ChangeContext})
			oldNum++
			newNum++
		default:
			// unknown prefix, treat as context
			lines = append(lines, DiffLine{OldNum: oldNum, NewNum: newNum, Content: line, ChangeType: ChangeContext})
			oldNum++
			newNum++
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}

	// trailing divider: unchanged lines after the last hunk on the old side.
	// Emitted only when the caller supplied totalOldLines AND at least one hunk
	// was processed. sawHunk (not prevOldEnd > 1) is the correct "processed" flag
	// - insertion-at-start hunks (@@ -0,0 ...) leave prevOldEnd at 1 but still count.
	if totalOldLines > 0 && sawHunk {
		lines = appendGapDivider(lines, totalOldLines-prevOldEnd+1)
	}

	return lines, nil
}

// normalizePrefixes trims whitespace and trailing slashes from each prefix,
// skipping empty values (e.g., from env var trailing commas).
func normalizePrefixes(prefixes []string) []string {
	normalized := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		p = strings.TrimRight(p, "/")
		if p == "" {
			continue
		}
		normalized = append(normalized, p)
	}
	return normalized
}

// matchesPrefix returns true if the file path matches any prefix.
// A prefix matches if the file equals the prefix exactly, or starts with prefix + "/".
func matchesPrefix(file string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if file == prefix || strings.HasPrefix(file, prefix+"/") {
			return true
		}
	}
	return false
}

// PathFilter returns the include/exclude predicate behind IncludeFilter,
// ExcludeFilter and FilterPaths: with include prefixes set a path must match
// one of them, and a path matching an exclude prefix is always rejected.
// Prefixes are normalized as in the filter wrappers. with neither list set the
// predicate accepts everything.
func PathFilter(include, exclude []string) func(path string) bool {
	inc := normalizePrefixes(include)
	exc := normalizePrefixes(exclude)
	return func(path string) bool {
		if len(inc) > 0 && !matchesPrefix(path, inc) {
			return false
		}
		return len(exc) == 0 || !matchesPrefix(path, exc)
	}
}

// FilterPaths keeps the paths permitted by the given include/exclude prefixes,
// mirroring IncludeFilter+ExcludeFilter semantics for path lists that bypass the
// DiffSource chain - notably untracked files, which come straight from the
// UntrackedFiles call and never pass through the filter wrappers. Empty
// include/exclude returns paths unchanged.
func FilterPaths(paths, include, exclude []string) []string {
	if len(normalizePrefixes(include)) == 0 && len(normalizePrefixes(exclude)) == 0 {
		return paths
	}
	keep := PathFilter(include, exclude)
	filtered := make([]string, 0, len(paths))
	for _, p := range paths {
		if keep(p) {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// ParseNameStatus parses NUL-separated `git diff --name-status -z` output into
// file entries. the package-level form for callers outside the Git reader.
func ParseNameStatus(out string) []FileEntry {
	return (&Git{}).parseNameStatusEntries(out)
}
