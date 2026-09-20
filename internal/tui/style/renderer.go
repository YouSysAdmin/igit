package style

import (
	"strings"

	"github.com/yousysadmin/igit/internal/git"
)

// Renderer produces complete rendered strings for specific UI widgets
// (annotation marks, cursor cells, status separators, file status marks).
// It composes multiple Color lookups into final ANSI-tagged strings.
type Renderer struct {
	res Resolver
}

// NewRenderer wires a Renderer to a Resolver. The caller is responsible
// for constructing the Resolver first (via NewResolver or PlainResolver).
func NewRenderer(res Resolver) Renderer {
	return Renderer{res: res}
}

// AnnotationInline renders an inline annotation with italic styling using raw ANSI sequences.
// uses AnnotationFg and DiffPaneBg from the resolver.
func (r Renderer) AnnotationInline(text string) string {
	fg := r.res.Color(ColorKeyAnnotationFg)
	bg := r.res.Color(ColorKeyDiffPaneBg)
	var b strings.Builder
	if fg != "" {
		b.WriteString(string(fg))
	}
	if bg != "" {
		b.WriteString(string(bg))
	}
	b.WriteString("\033[3m") // italic on
	b.WriteString(text)
	b.WriteString("\033[23m") // italic off
	if fg != "" {
		b.WriteString(string(ResetFg))
	}
	if bg != "" {
		b.WriteString(string(ResetBg))
	}
	return b.String()
}

// DiffCursor renders the cursor glyph using raw ANSI sequences instead of lipgloss.Render()
// to avoid \033[0m full reset that kills the outer DiffBg background.
// when noColors is true, falls back to reverse video.
func (r Renderer) DiffCursor(noColors bool) string {
	if noColors {
		return "\033[7m▶\033[27m" // reverse video
	}
	cursorFg := ansiColor(r.res.colors.CursorFg, 38)
	cursorBg := r.res.colors.CursorBg
	if cursorBg == "" {
		cursorBg = r.res.colors.DiffBg
	}
	bgSeq := ansiColor(cursorBg, 48)

	var b strings.Builder
	if cursorFg != "" {
		b.WriteString(cursorFg)
	}
	if bgSeq != "" {
		b.WriteString(bgSeq)
	}
	b.WriteString("▶")
	if cursorFg != "" {
		b.WriteString(string(ResetFg))
	}
	if bgSeq != "" {
		b.WriteString(string(ResetBg))
	}
	return b.String()
}

// SelectionMark renders the gutter glyph for a diff line inside a multi-line
// selection (line staging, stage marks). Raw ANSI like DiffCursor so the pane
// background survives. reverse video when colors are off.
func (r Renderer) SelectionMark(noColors bool) string {
	if noColors {
		return "\033[7m▎\033[27m"
	}
	fg := ansiColor(r.res.colors.Accent, 38)
	if fg == "" {
		return "▎"
	}
	return fg + "▎" + string(ResetFg)
}

// StageMark renders the gutter glyph for a diff line marked for the commit
// (review stage plan), in the add color.
func (r Renderer) StageMark(noColors bool) string {
	if noColors {
		return "\033[7m┃\033[27m"
	}
	fg := ansiColor(r.res.colors.AddFg, 38)
	if fg == "" {
		return "┃"
	}
	return fg + "┃" + string(ResetFg)
}

// FileStageMark returns the file-tree marker for a file with stage marks.
func (r Renderer) FileStageMark() string {
	fg := ansiColor(r.res.colors.AddFg, 38)
	if fg == "" {
		return " +"
	}
	return fg + " +" + string(ResetFg)
}

// StatusBarSeparator returns a separator string for the status bar, styled with
// muted foreground for the pipe and status foreground for the surrounding spaces.
func (r Renderer) StatusBarSeparator() string {
	muted := r.res.Color(ColorKeyMutedFg)
	statusFg := r.res.Color(ColorKeyStatusFg)
	return " " + string(muted) + "|" + string(statusFg) + " "
}

// FileStatusMark returns a colored file status character (A, M, D, ?, etc.)
// followed by a space, using raw ANSI sequences to avoid lipgloss full reset.
// restores the Normal foreground after the status character. falls back to
// ResetFg when Colors.Normal is empty to prevent the status color from
// bleeding into subsequent text on the same line.
func (r Renderer) FileStatusMark(status git.ChangeStatus) string {
	fg := r.fileStatusFg(status)
	if fg == "" {
		return string(status) + " "
	}
	normalFg := Color(ansiColor(r.res.colors.Normal, 38))
	if normalFg == "" {
		normalFg = ResetFg
	}
	return string(fg) + string(status) + string(normalFg) + " "
}

// FileReviewedMark returns a colored checkmark for reviewed files.
// uses AddFg for the checkmark and Normal for the reset. falls back to
// ResetFg when Colors.Normal is empty to prevent the AddFg color from
// bleeding into subsequent text on the same line.
func (r Renderer) FileReviewedMark() string {
	addFg := Color(ansiColor(r.res.colors.AddFg, 38))
	if addFg == "" {
		return "✓ "
	}
	normalFg := Color(ansiColor(r.res.colors.Normal, 38))
	if normalFg == "" {
		normalFg = ResetFg
	}
	return string(addFg) + "✓" + string(normalFg) + " "
}

// FileAnnotationMark returns a colored annotation marker for files with annotations.
// uses AnnotationFg with ResetFg after the marker.
func (r Renderer) FileAnnotationMark() string {
	annotFg := r.res.Color(ColorKeyAnnotationFg)
	if annotFg == "" {
		return " *"
	}
	return string(annotFg) + " *" + string(ResetFg)
}

// fileStatusFg returns the ANSI foreground color for a file status.
// FileAdded/FileUntracked -> AddFg, FileDeleted/FileUnmerged -> RemoveFg,
// default -> Muted. An unmerged path borrows the remove color, the way the
// commit-mode conflict section marks it.
func (r Renderer) fileStatusFg(status git.ChangeStatus) Color {
	switch status {
	case git.FileAdded, git.FileUntracked:
		return Color(ansiColor(r.res.colors.AddFg, 38))
	case git.FileDeleted, git.FileUnmerged:
		return Color(ansiColor(r.res.colors.RemoveFg, 38))
	default:
		return Color(ansiColor(r.res.colors.Muted, 38))
	}
}
