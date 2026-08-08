package core

import "fmt"

// splitHunkAtEntities splits a hunk into sub-hunks so that no sub-hunk spans
// more than one entity. Without this, a hunk that straddles two functions
// (context overlap between nearby changes, or a whole new file arriving as
// one hunk) collapses into a single snippet under just one of them —
// violating "never merge two distinct named entities".
//
// Deleted lines carry no post-image position; they stay with the segment
// that is open where they occur — except when they are immediately followed
// by added lines that open a new segment: such a -/+ pair is a replacement
// and must stay together, or accepting only one snippet would stage a
// deletion the reviewer never saw. Sub-hunk headers are regenerated with
// correct old/new starts and counts, so each sub-hunk remains a valid patch
// fragment for git apply.
func splitHunkAtEntities(h Hunk, entities []Entity) []Hunk {
	lines := h.Lines
	if len(lines) > 0 && len(lines[0]) >= 2 && lines[0][0] == '@' {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return []Hunk{h}
	}

	entityAt := func(line int) *Entity {
		var best *Entity
		for i := range entities {
			e := &entities[i]
			if e.StartLine <= line && line <= e.EndLine {
				if best == nil || (e.EndLine-e.StartLine) < (best.EndLine-best.StartLine) {
					best = e
				}
			}
		}
		return best
	}

	type segment struct {
		entity   *Entity
		oldStart int
		newStart int
		oldCount int
		newCount int
		lines    []string
	}

	var segs []*segment
	var cur *segment
	oldLine, newLine := h.OldStart, h.NewStart
	var pendingDel []string

	open := func(ent *Entity) {
		cur = &segment{entity: ent, oldStart: oldLine, newStart: newLine}
		segs = append(segs, cur)
	}
	flushDel := func() {
		for _, dl := range pendingDel {
			cur.lines = append(cur.lines, dl)
			cur.oldCount++
			oldLine++
		}
		pendingDel = nil
	}

	for _, l := range lines {
		prefix := byte(' ')
		if len(l) > 0 {
			prefix = l[0]
		}
		switch prefix {
		case '-':
			// Held back until the next post-image line decides which
			// segment the deletion belongs to.
			if cur == nil {
				open(entityAt(newLine))
			}
			pendingDel = append(pendingDel, l)
		case '+':
			// A replacement: pending deletions move with their '+' lines.
			ent := entityAt(newLine)
			if cur == nil || ent != cur.entity {
				open(ent)
			}
			flushDel()
			cur.lines = append(cur.lines, l)
			cur.newCount++
			newLine++
		default:
			// Context: pending deletions stay with the open segment.
			if cur == nil {
				open(entityAt(newLine))
			}
			flushDel()
			if ent := entityAt(newLine); ent != cur.entity {
				open(ent)
			}
			cur.lines = append(cur.lines, l)
			cur.oldCount++
			cur.newCount++
			oldLine++
			newLine++
		}
	}
	if cur != nil {
		flushDel()
	}

	if len(segs) <= 1 {
		return []Hunk{h}
	}

	out := make([]Hunk, 0, len(segs))
	for _, s := range segs {
		header := fmt.Sprintf("@@ -%d,%d +%d,%d @@", s.oldStart, s.oldCount, s.newStart, s.newCount)
		out = append(out, Hunk{
			OldStart: s.oldStart, OldLines: s.oldCount,
			NewStart: s.newStart, NewLines: s.newCount,
			Lines: append([]string{header}, s.lines...),
		})
	}
	return out
}
