package patch

import "strings"

// formatPlain writes the patch back as text. A patch without any change is
// empty: git rejects a patch made only of context.
func formatPlain(p *Patch) string {
	if !p.ContainsChanges() {
		return ""
	}
	var b strings.Builder
	for _, line := range p.header {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, hunk := range p.hunks {
		b.WriteString(hunk.formatHeaderLine())
		b.WriteByte('\n')
		for _, line := range hunk.bodyLines {
			b.WriteString(line.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// formatRangePlain writes the lines startIdx..endIdx (inclusive) as text.
func formatRangePlain(p *Patch, startIdx, endIdx int) string {
	lines := p.Lines()
	startIdx = max(startIdx, 0)
	endIdx = min(endIdx, len(lines)-1)
	var b strings.Builder
	for i := startIdx; i <= endIdx; i++ {
		b.WriteString(lines[i].Content)
		b.WriteByte('\n')
	}
	return b.String()
}
