package tui

import (
	"github.com/yousysadmin/igit/internal/git"
)

// remoteNoteMarker leads a comment that came from the request itself. The
// session's own annotations keep the configured marker, so two comments on one
// line stay told apart.
const remoteNoteMarker = "\U0001f464"

// RemoteNote is a review comment that already exists on the request. It is
// drawn under its line like an annotation but lives outside the store, so it
// is never written to the output or posted again.
type RemoteNote struct {
	File   string
	Line   int    // line on Side (GitHub numbering: new file for RIGHT, old file for LEFT)
	Side   string // "LEFT" for removed lines, "RIGHT" (or empty) otherwise
	Author string
	Body   string
}

// text renders the note the way it appears under a diff line.
func (n RemoteNote) text() string {
	if n.Author == "" {
		return n.Body
	}
	return "@" + n.Author + ": " + n.Body
}

// groupRemoteNotes indexes notes by file.
func groupRemoteNotes(notes []RemoteNote) map[string][]RemoteNote {
	if len(notes) == 0 {
		return nil
	}
	byFile := make(map[string][]RemoteNote)
	for _, n := range notes {
		byFile[n.File] = append(byFile[n.File], n)
	}
	return byFile
}

// remoteNotePrefix leads a remote comment row.
func (m Model) remoteNotePrefix() string { return remoteNoteMarker + " " }

// indexRemoteNotes anchors the current file's remote comments to annotation
// keys, the same keys the store's annotations use. A note is placed through the
// loaded diff lines: LEFT notes by old line number, RIGHT notes by new line
// number. Notes whose line is not in the loaded diff are dropped. handleFileLoaded
// rebuilds this, the notes of a request never change during a session.
func (m Model) indexRemoteNotes() map[string][]RemoteNote {
	notes := m.remoteNotes[m.file.name]
	if len(notes) == 0 {
		return nil
	}
	byOld := make(map[int]string)
	byNew := make(map[int]string)
	for _, dl := range m.file.lines {
		switch dl.ChangeType { //nolint:exhaustive // dividers and placeholders carry no line numbers
		case git.ChangeRemove:
			byOld[dl.OldNum] = m.annotationKey(dl.OldNum, string(dl.ChangeType))
		case git.ChangeAdd:
			byNew[dl.NewNum] = m.annotationKey(dl.NewNum, string(dl.ChangeType))
		case git.ChangeContext:
			key := m.annotationKey(dl.NewNum, string(dl.ChangeType))
			byNew[dl.NewNum] = key
			if _, taken := byOld[dl.OldNum]; !taken {
				byOld[dl.OldNum] = key
			}
		}
	}
	index := make(map[string][]RemoteNote, len(notes))
	for _, n := range notes {
		side := byNew
		if n.Side == "LEFT" {
			side = byOld
		}
		if key, ok := side[n.Line]; ok {
			index[key] = append(index[key], n)
		}
	}
	return index
}
