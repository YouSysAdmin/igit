package tui

// Mode identifies which top-level TUI mode owns input and rendering.
type Mode int

// TUI modes. ModeReview is the diff reviewer with inline annotations. ModeCommit
// is the git staging/commit workspace.
const (
	ModeReview Mode = iota
	ModeCommit
)

// String returns the lower-case mode name used in flags and status text.
func (m Mode) String() string {
	switch m {
	case ModeReview:
		return "review"
	case ModeCommit:
		return "commit"
	}
	return "unknown"
}

// ParseMode maps a flag value to a Mode. ok is false for unknown names.
func ParseMode(s string) (mode Mode, ok bool) {
	switch s {
	case "review", "":
		return ModeReview, true
	case "commit":
		return ModeCommit, true
	}
	return ModeReview, false
}
