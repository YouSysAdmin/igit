// Package keymap provides user-configurable key bindings for igit.
// It maps key names (as returned by bubbletea's KeyMsg.String()) to action names.
package keymap

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"unicode/utf8"
)

// HelpEntry describes a single action for the help overlay.
type HelpEntry struct {
	Action      Action
	Description string
	Section     string
}

// HelpSection groups help entries under a section heading.
type HelpSection struct {
	Name    string
	Entries []HelpEntryWithKeys
}

// HelpEntryWithKeys is a help entry with the effective key bindings attached.
type HelpEntryWithKeys struct {
	Action      Action
	Description string
	Keys        string // formatted as "key1 / key2"
}

// Keymap maps key names to actions. Keys are stored as the string returned
// by bubbletea's tea.KeyMsg.String().
type Keymap struct {
	bindings         map[string]Action
	descriptions     []HelpEntry         // ordered list of action descriptions
	chordPrefixCache map[string]struct{} // lazy cache of chord leader keys, nil = not yet built
}

// NormalizeKey returns the Latin QWERTY equivalent of a single non-Latin key
// character, or the input unchanged when it has no mapping. Multi-character key
// strings (e.g. "esc", "ctrl+w") pass through unchanged. Used by dispatch paths
// that compare keys against literal bindings without going through Resolve, so
// non-Latin layouts behave identically to Latin ones.
func NormalizeKey(key string) string {
	if r, size := utf8.DecodeRuneInString(key); size == len(key) {
		if alias, ok := layoutResolve(r); ok {
			return string(alias)
		}
	}
	return key
}

// Resolve returns the action bound to the given key, or empty Action if unbound.
// For non-Latin keyboard layouts, if the key has no direct binding, it is
// translated to its Latin QWERTY equivalent and looked up again.
func (km *Keymap) Resolve(key string) Action {
	if a, ok := km.bindings[key]; ok {
		return a
	}
	// fallback: translate non-Latin character to Latin equivalent
	if r, size := utf8.DecodeRuneInString(key); size == len(key) {
		if alias, ok := layoutResolve(r); ok {
			if a, ok := km.bindings[string(alias)]; ok {
				return a
			}
		}
	}
	return ""
}

// ResolveChord returns the action bound to the chord (prefix, second), or empty
// Action if unbound. Applies a layout-resolve fallback to the second key: when
// the direct lookup misses and the second key is a single rune, the rune is
// translated to its Latin QWERTY equivalent and the lookup is retried.
func (km *Keymap) ResolveChord(prefix, second string) Action {
	if a, ok := km.bindings[prefix+">"+second]; ok {
		return a
	}
	if r, size := utf8.DecodeRuneInString(second); size == len(second) {
		if alias, ok := layoutResolve(r); ok {
			if a, ok := km.bindings[prefix+">"+string(alias)]; ok {
				return a
			}
		}
	}
	return ""
}

// KeysFor returns all keys bound to the given action, sorted alphabetically.
func (km *Keymap) KeysFor(action Action) []string {
	var keys []string
	for k, a := range km.bindings {
		if a == action {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// Bind maps a key to an action, overriding any previous binding for that key.
func (km *Keymap) Bind(key string, action Action) {
	km.bindings[key] = action
	km.chordPrefixCache = nil
}

// Unbind removes the binding for the given key. No-op if key is not bound.
func (km *Keymap) Unbind(key string) {
	delete(km.bindings, key)
	km.chordPrefixCache = nil
}

// chordPrefixes returns the set of leader keys that have at least one chord binding.
// The result is built lazily on first call and cached until a Bind/Unbind invalidates it.
func (km *Keymap) chordPrefixes() map[string]struct{} {
	if km.chordPrefixCache != nil {
		return km.chordPrefixCache
	}
	cache := make(map[string]struct{})
	for k := range km.bindings {
		leader, _, ok := strings.Cut(k, ">")
		if !ok || leader == "" {
			continue
		}
		cache[leader] = struct{}{}
	}
	km.chordPrefixCache = cache
	return cache
}

// IsChordLeader returns true if the given key is the leader of any chord binding.
// Lookup is O(1) via a cached prefix index, built on first call.
func (km *Keymap) IsChordLeader(key string) bool {
	_, ok := km.chordPrefixes()[key]
	return ok
}

// HelpSections returns grouped help entries with effective key bindings.
// Actions with no bound keys are omitted.
func (km *Keymap) HelpSections() []HelpSection {
	// collect unique sections in order
	var sections []HelpSection
	sectionIdx := make(map[string]int)

	for _, desc := range km.descriptions {
		keys := km.KeysFor(desc.Action)
		if len(keys) == 0 {
			continue // skip unmapped actions
		}

		// the raw binding of the space bar is " ", which would leave the help's
		// key column looking empty. DisplayKey names it the way the keybindings
		// file does.
		shown := make([]string, 0, len(keys))
		for _, k := range keys {
			shown = append(shown, DisplayKey(k))
		}
		entry := HelpEntryWithKeys{
			Action:      desc.Action,
			Description: desc.Description,
			Keys:        strings.Join(shown, " / "),
		}

		idx, exists := sectionIdx[desc.Section]
		if !exists {
			idx = len(sections)
			sectionIdx[desc.Section] = idx
			sections = append(sections, HelpSection{Name: desc.Section})
		}
		sections[idx].Entries = append(sections[idx].Entries, entry)
	}

	return sections
}

// Dump writes the effective bindings to w in the keybindings file format,
// grouped by section with # comments. Output can be loaded back with Load.
func (km *Keymap) Dump(w io.Writer) error {
	return km.dump(w, "")
}

// dump writes the effective bindings with every key prefixed by prefix
// ("commit:" for the commit-mode map, empty for the review map).
func (km *Keymap) dump(w io.Writer, prefix string) error {
	sections := km.HelpSections()
	for i, sec := range sections {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return fmt.Errorf("dump keybindings: %w", err)
			}
		}
		if _, err := fmt.Fprintf(w, "# %s\n", sec.Name); err != nil {
			return fmt.Errorf("dump keybindings: %w", err)
		}
		for _, entry := range sec.Entries {
			keys := km.KeysFor(entry.Action)
			for _, k := range keys {
				if _, err := fmt.Fprintf(w, "map %s%s %s\n", prefix, km.dumpKeyName(k), entry.Action); err != nil {
					return fmt.Errorf("dump keybindings: %w", err)
				}
			}
		}
	}
	return nil
}

// reverseAliases maps canonical bubbletea key strings back to user-friendly names
// for keys that would not survive a round-trip through strings.Fields.
var reverseAliases = map[string]string{
	" ": "space",
}

// DisplayKey converts a canonical key string to the user-friendly name used in
// help, legends and the keybindings file ("space" for " ". chords keep ">").
func DisplayKey(key string) string {
	if leader, second, ok := strings.Cut(key, ">"); ok {
		return DisplayKey(leader) + ">" + DisplayKey(second)
	}
	if alias, ok := reverseAliases[key]; ok {
		return alias
	}
	return key
}

// dumpKeyName converts a canonical key string to a user-friendly name for dump output.
// keys that are whitespace-only need special handling so the output can be reloaded.
// chord keys are split on ">" and each half is dumped independently so that an embedded
// literal space in either half is rewritten to its "space" alias, preserving the round-trip.
func (km *Keymap) dumpKeyName(key string) string { return DisplayKey(key) }

// mapEntry represents a parsed "map <key> <action>" line. mode is "" for the
// review keymap and modeCommit for a "commit:"-prefixed key.
type mapEntry struct {
	key    string
	action Action
	mode   string
}

// modeCommit is the key prefix (without the colon) that targets the commit keymap.
const modeCommit = "commit"

// splitMode strips an optional "<mode>:" prefix from a raw key. Only the
// commit mode is recognized. anything else is returned unchanged so keys that
// legitimately contain a colon keep working.
func splitMode(rawKey string) (mode, key string) {
	if rest, ok := strings.CutPrefix(rawKey, modeCommit+":"); ok && rest != "" {
		return modeCommit, rest
	}
	return "", rawKey
}

// keyAliases maps user-friendly key names to bubbletea's KeyMsg.String() output.
// keys that already match bubbletea's output are not listed here.
var keyAliases = map[string]string{
	"page_down":  "pgdown",
	"page_up":    "pgup",
	"pagedown":   "pgdown",
	"pageup":     "pgup",
	"escape":     "esc",
	"return":     "enter",
	"space":      " ",
	"ctrl+enter": "ctrl+m", // bubbletea maps enter to ctrl+m internally
	"meta+enter": "alt+enter",
}

// normalizeKey converts a user-provided key name to the canonical form
// used by bubbletea's KeyMsg.String(). Returns the normalized key.
func normalizeKey(key string) string {
	lower := strings.ToLower(key)
	if alias, ok := keyAliases[lower]; ok {
		return alias
	}
	// ctrl+ prefixed keys are always lowercase in bubbletea
	if strings.HasPrefix(lower, "ctrl+") {
		return lower
	}
	// alt+ prefixed keys: lowercase only the "alt+" prefix. preserve post-prefix
	// case because bubbletea distinguishes alt+t from alt+T (shift-modifier matters)
	if strings.HasPrefix(lower, "alt+") {
		return "alt+" + key[4:]
	}
	// preserve original case for single chars (j vs J matters)
	return key
}

// parseChordKey validates and normalizes a chord key of the form "<leader>><second>".
// Returns the normalized chord key and true on success, or "" and false after logging
// a warning for empty halves, three-stage chords, non-ctrl/alt leaders, or esc as the
// second-stage key (reserved for cancel). Leader case is normalized via normalizeKey
// (ctrl+/alt+ lowercased). second-stage case is preserved so that ctrl+w>x and ctrl+w>X
// remain distinct.
func parseChordKey(rawKey string, lineNum int) (string, bool) {
	parts := strings.SplitN(rawKey, ">", 2)
	leader, second := parts[0], parts[1]
	if leader == "" || second == "" {
		log.Printf("[WARN] keybindings:%d: chord halves cannot be empty, skipping", lineNum)
		return "", false
	}
	if strings.Contains(second, ">") {
		log.Printf("[WARN] keybindings:%d: only 2-stage chords supported, skipping", lineNum)
		return "", false
	}
	leaderNorm := normalizeKey(leader)
	if !strings.HasPrefix(leaderNorm, "ctrl+") && !strings.HasPrefix(leaderNorm, "alt+") {
		log.Printf("[WARN] keybindings:%d: chord leader must be ctrl+ or alt+ combo, skipping", lineNum)
		return "", false
	}
	secondNorm := normalizeKey(second)
	if secondNorm == "esc" {
		log.Printf("[WARN] keybindings:%d: esc cannot be a chord second-stage key (reserved for cancel), skipping", lineNum)
		return "", false
	}
	return leaderNorm + ">" + secondNorm, true
}

// parse reads keybinding definitions from r and returns the review-mode map
// entries and unmap keys. commit-mode ("commit:"-prefixed) lines are dropped.
// See parseModes for the full result.
func parse(r io.Reader) (maps []mapEntry, unmaps []string, err error) {
	allMaps, allUnmaps, err := parseModes(r)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range allMaps {
		if m.mode == "" {
			maps = append(maps, m)
		}
	}
	for _, u := range allUnmaps {
		if u.mode == "" {
			unmaps = append(unmaps, u.key)
		}
	}
	return maps, unmaps, nil
}

// parseModes reads keybinding definitions from r and returns map and unmap entries
// for both modes. format: "map <key> <action>" or "unmap <key>", where <key> may carry
// a "commit:" prefix to target the commit keymap. # comments and blank lines are
// ignored. Unknown action names are reported via log and skipped. Duplicate
// mappings: last wins. Unmap entries reuse mapEntry with an empty action.
func parseModes(r io.Reader) (maps, unmaps []mapEntry, err error) {
	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			log.Printf("[WARN] keybindings:%d: invalid line %q, skipping", lineNum, line)
			continue
		}

		cmd := strings.ToLower(fields[0])
		switch cmd {
		case "map":
			if len(fields) < 3 {
				log.Printf("[WARN] keybindings:%d: map requires key and action, skipping", lineNum)
				continue
			}
			mode, rawKey := splitMode(fields[1])
			rawAction := Action(fields[2])
			action, deprecated, ok := resolveAction(rawAction)
			if !ok {
				log.Printf("[WARN] keybindings:%d: unknown action %q, skipping", lineNum, rawAction)
				continue
			}
			if deprecated {
				warnOnceDeprecatedAlias(rawAction, action)
			}
			if strings.Contains(rawKey, ">") && rawKey != ">" {
				key, ok := parseChordKey(rawKey, lineNum)
				if !ok {
					continue
				}
				maps = append(maps, mapEntry{key: key, action: action, mode: mode})
				continue
			}
			maps = append(maps, mapEntry{key: normalizeKey(rawKey), action: action, mode: mode})
		case "unmap":
			mode, rawKey := splitMode(fields[1])
			if strings.Contains(rawKey, ">") && rawKey != ">" {
				key, ok := parseChordKey(rawKey, lineNum)
				if !ok {
					continue
				}
				unmaps = append(unmaps, mapEntry{key: key, mode: mode})
				continue
			}
			unmaps = append(unmaps, mapEntry{key: normalizeKey(rawKey), mode: mode})
		default:
			log.Printf("[WARN] keybindings:%d: unknown command %q in line %q, skipping", lineNum, cmd, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading keybindings: %w", err)
	}
	return maps, unmaps, nil
}

// Load reads a keybindings file from path and returns a Keymap with defaults
// overridden by the file contents. Returns error if the file cannot be opened or parsed.
func Load(path string) (*Keymap, error) {
	f, err := os.Open(path) //nolint:gosec // path is user-provided config file location
	if err != nil {
		return nil, fmt.Errorf("opening keybindings file: %w", err)
	}
	defer f.Close()

	maps, unmaps, err := parse(f)
	if err != nil {
		return nil, err
	}

	km := Default()

	// apply unmaps first, then maps (so "unmap q" + "map x quit" works)
	for _, key := range unmaps {
		km.Unbind(key)
	}
	for _, m := range maps {
		km.Bind(m.key, m.action)
	}

	km.resolveConflicts()
	return km, nil
}

// resolveConflicts drops any standalone binding whose key is also a chord leader.
// When both "ctrl+w" and "ctrl+w>x" exist, the standalone is removed with a warning
// so that pressing the leader always enters chord-pending state instead of firing
// the standalone action. Invalidates the chord-prefix cache once at the end.
func (km *Keymap) resolveConflicts() {
	for chordKey := range km.bindings {
		leader, _, ok := strings.Cut(chordKey, ">")
		if !ok || leader == "" {
			continue
		}
		if _, exists := km.bindings[leader]; exists {
			log.Printf("[WARN] keybindings: %s bound as both standalone and chord prefix, standalone dropped", leader)
			delete(km.bindings, leader)
		}
	}
	km.chordPrefixCache = nil
}

// LoadOrDefault loads keybindings from path if the file exists, otherwise returns
// Default(). Parse errors are logged as warnings and Default() is returned.
func LoadOrDefault(path string) *Keymap {
	if path == "" {
		return Default()
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Default()
	}
	km, err := Load(path)
	if err != nil {
		log.Printf("[WARN] failed to load keybindings from %s: %v, using defaults", path, err)
		return Default()
	}
	return km
}
