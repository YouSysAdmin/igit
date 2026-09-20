package keymap

import (
	"fmt"
	"io"
	"log"
	"os"
)

// One keybindings file describes both modes: unprefixed lines are review's,
// "commit:"-prefixed ones are commit's. Set is what the composition root hands
// to the two models.

// Set holds the keymaps of both TUI modes, loaded from one keybindings file.
// Unprefixed "map"/"unmap" lines target Review. "commit:"-prefixed keys target Commit.
type Set struct {
	Review *Keymap
	Commit *Keymap
}

// DefaultSet returns the default keymaps of both modes.
func DefaultSet() *Set {
	return &Set{Review: Default(), Commit: DefaultCommit()}
}

// LoadSet reads a keybindings file from path and returns both keymaps with
// defaults overridden by the file contents.
func LoadSet(path string) (*Set, error) {
	f, err := os.Open(path) //nolint:gosec // path is user-provided config file location
	if err != nil {
		return nil, fmt.Errorf("opening keybindings file: %w", err)
	}
	defer f.Close()

	maps, unmaps, err := parseModes(f)
	if err != nil {
		return nil, err
	}

	set := DefaultSet()
	// apply unmaps first, then maps (so "unmap q" + "map x quit" works)
	for _, u := range unmaps {
		set.forMode(u.mode).Unbind(u.key)
	}
	for _, m := range maps {
		set.forMode(m.mode).Bind(m.key, m.action)
	}
	set.Review.resolveConflicts()
	set.Commit.resolveConflicts()
	return set, nil
}

// LoadSetOrDefault loads both keymaps from path if the file exists, otherwise
// returns DefaultSet(). Parse errors are logged as warnings and defaults returned.
func LoadSetOrDefault(path string) *Set {
	if path == "" {
		return DefaultSet()
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return DefaultSet()
	}
	set, err := LoadSet(path)
	if err != nil {
		log.Printf("[WARN] failed to load keybindings from %s: %v, using defaults", path, err)
		return DefaultSet()
	}
	return set
}

// forMode returns the keymap targeted by a parsed mode prefix.
func (s *Set) forMode(mode string) *Keymap {
	if mode == modeCommit {
		return s.Commit
	}
	return s.Review
}

// Dump writes the effective bindings of both modes in the keybindings file
// format: the review map first, then the commit map with "commit:"-prefixed keys.
func (s *Set) Dump(w io.Writer) error {
	if err := s.Review.dump(w, ""); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\n# commit mode (keys prefixed with %s:)\n\n", modeCommit); err != nil {
		return fmt.Errorf("dump keybindings: %w", err)
	}
	return s.Commit.dump(w, modeCommit+":")
}
