package core

import (
	"fmt"
	"strings"
)

// splitHunkAtEntities splits a hunk into sub-hunks so that no sub-hunk spans
// more than one entity. Without this, a hunk that straddles two functions
// (context overlap between nearby changes, or a whole new file arriving as
// one hunk) collapses into a single snippet under just one of them —
// violating "never merge two distinct named entities".
//
// Deleted lines carry no post-image position; they stay with the segment
// that is open where they occur — except when they are immediately followed
// by added lines: such a -/+ pair is a replacement and must stay together,
// or accepting only one snippet would stage a deletion the reviewer never
// saw. A blank changed line goes with the code it touches, so blank
// separators stage with the function they belong to. An unchanged line
// that git paired with an equal line of a neighbouring entity is paired
// back, so each entity keeps its own boundary lines. A "\ No newline at
// end of file" marker stays with the line it annotates.
//
// Every changed line lands in exactly one piece, so applying any set of
// pieces to the old file is well defined. Sub-hunk headers carry each
// piece's exact old and new start and line counts; a side with no lines
// names the line after which the piece applies (0: before the first line),
// as git does.
//
// Each piece reports the entity it was cut for (nil outside every entity),
// so callers group pieces by entity identity rather than by name.
func splitHunkAtEntities(h Hunk, entities []Entity) []piece {
	body := h.Lines
	if len(body) > 0 && strings.HasPrefix(body[0], "@@") {
		body = body[1:]
	}
	if len(body) == 0 {
		return []piece{{Hunk: h}}
	}

	s := &hunkSplit{entities: entities, lines: make([]diffLine, len(body))}
	for i, l := range body {
		s.lines[i] = diffLine{text: l, kind: lineKind(l)}
	}
	tail, same := s.eofTail()
	oldNext, newNext := h.OldStart, h.NewStart
	if h.OldLines == 0 {
		oldNext++
	}
	if h.NewLines == 0 {
		newNext++
	}
	s.number(oldNext, newNext)
	if s.slide() {
		s.number(oldNext, newNext)
	}
	s.assignOwners(tail, same)

	segs := s.segments()
	if len(segs) == 0 {
		return []piece{{Hunk: h}}
	}
	if len(segs) == 1 {
		return []piece{{Hunk: h, entity: segs[0].entity}}
	}
	out := make([]piece, 0, len(segs))
	for _, sg := range segs {
		header := fmt.Sprintf("@@ -%d,%d +%d,%d @@", sg.oldStart, sg.oldCount, sg.newStart, sg.newCount)
		out = append(out, piece{Hunk: Hunk{
			OldStart: sg.oldStart, OldLines: sg.oldCount,
			NewStart: sg.newStart, NewLines: sg.newCount,
			Lines: append([]string{header}, sg.lines...),
		}, entity: sg.entity})
	}
	return out
}

// piece is one sub-hunk of a split hunk and the entity it belongs to (nil
// when it lies outside every entity).
type piece struct {
	Hunk
	entity *Entity
}

// diffLine is one line of a hunk body.
type diffLine struct {
	text  string // the line with its sigil
	kind  byte   // ' ', '+', '-', or '\\' for "\ No newline at end of file"
	old   int    // context and '-': its old line; '+': the next old line
	new   int    // context and '+': its new line; '-': the next new line
	owner *Entity
}

func lineKind(l string) byte {
	if l != "" && (l[0] == '+' || l[0] == '-' || l[0] == '\\') {
		return l[0]
	}
	return ' '
}

// hunkSplit holds one hunk body while its lines are given owners.
type hunkSplit struct {
	entities []Entity
	lines    []diffLine
}

// leadInto returns the entity that a line at post-image position p, outside
// every entity, leads into: the nearest one starting at or after p, else
// (at the end of the file) the nearest one ending before it.
func (s *hunkSplit) leadInto(p int) *Entity {
	var next, prev *Entity
	for i := range s.entities {
		e := &s.entities[i]
		size := e.EndLine - e.StartLine
		switch {
		case e.StartLine >= p:
			if next == nil || e.StartLine < next.StartLine ||
				e.StartLine == next.StartLine && size < next.EndLine-next.StartLine {
				next = e
			}
		case e.EndLine < p:
			if prev == nil || e.EndLine > prev.EndLine ||
				e.EndLine == prev.EndLine && size < prev.EndLine-prev.StartLine {
				prev = e
			}
		}
	}
	if next != nil {
		return next
	}
	return prev
}

func blank(l diffLine) bool {
	return l.kind != ' ' && strings.TrimSpace(l.text[1:]) == ""
}

// content is a line without its sigil.
func content(l diffLine) string {
	if l.text == "" {
		return ""
	}
	return l.text[1:]
}

// entityAt returns the smallest entity containing a post-image line.
func (s *hunkSplit) entityAt(line int) *Entity {
	var best *Entity
	for i := range s.entities {
		e := &s.entities[i]
		if e.StartLine <= line && line <= e.EndLine {
			if best == nil || (e.EndLine-e.StartLine) < (best.EndLine-best.StartLine) {
				best = e
			}
		}
	}
	return best
}

// number gives each line its old and new position.
func (s *hunkSplit) number(oldNext, newNext int) {
	for i := range s.lines {
		l := &s.lines[i]
		l.old, l.new = oldNext, newNext
		switch l.kind {
		case ' ':
			oldNext++
			newNext++
		case '-':
			oldNext++
		case '+':
			newNext++
		}
	}
}

// eofTail handles an old file that ends without a newline: its last line
// is deleted ('-' then the marker) and anything added after it can only
// be applied together with the change that gives that line a newline.
// Those lines form one unit, the tail, which belongs to a single piece.
//
// When the line comes back with the same text (only its newline added), the
// deletion moves next to the line that re-adds it and the tail starts there,
// so a function appended at the end carries the newline it needs and the
// last line's own entity keeps nothing. It returns where the tail starts
// (-1 for none) and whether the line came back unchanged.
func (s *hunkSplit) eofTail() (start int, same bool) {
	L := s.lines
	for i := 0; i+1 < len(L); i++ {
		if L[i].kind != '-' || L[i+1].kind != '\\' {
			continue
		}
		for j := i + 2; j < len(L); j++ {
			if L[j].kind == '+' && L[j].text[1:] == L[i].text[1:] {
				moved := []diffLine{L[i], L[i+1]}
				copy(L[i:], L[i+2:j])
				copy(L[j-2:], moved)
				return j - 2, true
			}
		}
		b := i
		for b > 0 && L[b-1].kind != ' ' {
			b--
		}
		return b, false
	}
	return -1, false
}

// slide re-pairs an unchanged line that a run of added lines could match
// as well. git aligns an added block with the nearest equal line, so the
// closing line of a new function can take the old closing line of the
// function above it, which then gets an added line of its own:
//
//	 func alpha() int {
//	-	return x + y
//	+	return x * y
//	+}
//	+
//	+func helper() int {
//	+	return 42
//	 }
//
// Pairing the old "}" with the first added "}" is the same change, and each
// function keeps its own closing line, so either one staged alone is whole.
// Likewise a line before a pure insertion (a decorator the new function
// shares with the next one) moves to its end. A line is re-paired only
// from the entity at one end of the run to the entity at the other end,
// onto an equal added line of that entity. It reports whether any line
// moved.
func (s *hunkSplit) slide() bool {
	L := s.lines
	moved := false
	for b := 0; b < len(L); {
		if L[b].kind == ' ' {
			b++
			continue
		}
		e := b
		for e < len(L) && L[e].kind != ' ' {
			e++
		}
		switch {
		case s.slideDown(b, e):
			moved = true
			e++ // the line moved down is now added; it starts no run of its own
		case s.slideUp(b, e):
			moved = true
		}
		b = e
	}
	return moved
}

// runEnds returns the entities of the first and last non-blank lines added
// in the run L[b:e], and whether the run holds a marker.
func (s *hunkSplit) runEnds(b, e int) (first, last *Entity, marker bool) {
	seen := false
	for _, l := range s.lines[b:e] {
		switch {
		case l.kind == '\\':
			marker = true
		case l.kind == '+' && !blank(l):
			last = s.entityAt(l.new)
			if !seen {
				first, seen = last, true
			}
		}
	}
	return first, last, marker
}

// slideDown pairs the context line after the run L[b:e] with the last
// equal line added in the run's first entity, when the context line sits
// in the run's last entity.
func (s *hunkSplit) slideDown(b, e int) bool {
	L := s.lines
	if e >= len(L) || L[e].kind != ' ' || strings.TrimSpace(content(L[e])) == "" ||
		e+1 < len(L) && L[e+1].kind == '\\' {
		return false
	}
	first, last, marker := s.runEnds(b, e)
	at := s.entityAt(L[e].new)
	if marker || first == nil || at != last || at == first {
		return false
	}
	text := content(L[e])
	for k := e - 1; k >= b && L[k].kind == '+'; k-- {
		if content(L[k]) == text && s.entityAt(L[k].new) == first {
			L[k].kind, L[k].text = ' ', " "+text
			L[e].kind, L[e].text = '+', "+"+text
			return true
		}
	}
	return false
}

// slideUp pairs the context line before the pure insertion L[b:e] with the
// first equal line added in the run's last entity, when the context line
// sits in the run's first entity.
func (s *hunkSplit) slideUp(b, e int) bool {
	L := s.lines
	if b == 0 || L[b-1].kind != ' ' || strings.TrimSpace(content(L[b-1])) == "" {
		return false
	}
	for _, l := range L[b:e] {
		if l.kind != '+' {
			return false
		}
	}
	first, last, _ := s.runEnds(b, e)
	at := s.entityAt(L[b-1].new)
	if last == nil || at != first || at == last {
		return false
	}
	text := content(L[b-1])
	for k := b; k < e; k++ {
		if content(L[k]) == text && s.entityAt(L[k].new) == last {
			L[b-1].kind, L[b-1].text = '+', "+"+text
			L[k].kind, L[k].text = ' ', " "+text
			return true
		}
	}
	return false
}

// assignOwners gives every line the entity whose piece it goes to.
func (s *hunkSplit) assignOwners(tail int, same bool) {
	L := s.lines
	for i := range L {
		if L[i].kind == ' ' || L[i].kind == '+' {
			L[i].owner = s.entityAt(L[i].new)
		}
	}
	end := len(L)
	if same {
		end = tail
	}
	for b := 0; b < end; {
		if L[b].kind == ' ' {
			b++
			continue
		}
		e := b
		for e < end && L[e].kind != ' ' {
			e++
		}
		s.blankOwners(L[b:e])
		b = e
	}
	// A run of deletions goes with the added line right after it (a
	// replacement stays together), else with the line before it. Deleted
	// blank lines alone go where a blank line there would.
	for i := 0; i < end; {
		if L[i].kind != '-' {
			i++
			continue
		}
		j := i
		for j < end && (L[j].kind == '-' || L[j].kind == '\\') {
			j++
		}
		var owner *Entity
		switch {
		case j < end && L[j].kind == '+':
			owner = L[j].owner
		case allBlank(L[i:j]):
			if owner = s.entityAt(L[i].new); owner == nil {
				owner = s.leadInto(L[i].new)
			}
		case i > 0:
			owner = L[i-1].owner
		default:
			owner = s.entityAt(L[i].new)
		}
		for k := i; k < j; k++ {
			L[k].owner = owner
		}
		i = j
	}
	if tail >= 0 {
		owner := L[tail].owner
		if same {
			// The re-added last line, unless something non-blank follows
			// it: then the tail is that appended code's.
			owner = L[tail+2].owner
			for _, l := range L[tail+3:] {
				if l.kind == '+' && strings.TrimSpace(l.text[1:]) != "" {
					owner = l.owner
					break
				}
			}
		}
		for k := tail; k < len(L); k++ {
			L[k].owner = owner
		}
	}
	for i := 1; i < len(L); i++ {
		if L[i].kind == '\\' {
			L[i].owner = L[i-1].owner
		}
	}
}

// blankOwners gives the blank lines added in one run of changed lines the
// owner of the next non-blank line added in the run, else of the one before
// it: a separator stages with the code it separates, and the blank after a
// new function never turns the unchanged function below into a snippet.
// A run of nothing but blank lines keeps the entity it sits in, else joins
// the entity it leads into.
func (s *hunkSplit) blankOwners(run []diffLine) {
	var prev *Entity
	seen := false
	for i := range run {
		if run[i].kind != '+' {
			continue
		}
		if !blank(run[i]) {
			prev, seen = run[i].owner, true
			continue
		}
		next := -1
		for j := i + 1; j < len(run); j++ {
			if run[j].kind == '+' && !blank(run[j]) {
				next = j
				break
			}
		}
		switch {
		case next >= 0:
			run[i].owner = run[next].owner
		case seen:
			run[i].owner = prev
		case run[i].owner == nil:
			run[i].owner = s.leadInto(run[i].new)
		}
	}
}

func allBlank(run []diffLine) bool {
	for _, l := range run {
		if l.kind == '-' && !blank(l) {
			return false
		}
	}
	return true
}

// segment is a maximal run of lines with one owner.
type segment struct {
	entity                                 *Entity
	lines                                  []string
	oldStart, oldCount, newStart, newCount int
}

func (s *hunkSplit) segments() []segment {
	var segs []segment
	var cur *segment
	var oldFirst, newFirst int
	for i, l := range s.lines {
		if cur == nil || (l.owner != cur.entity && l.kind != '\\') {
			if cur != nil {
				cur.finish(oldFirst, newFirst)
			}
			segs = append(segs, segment{entity: l.owner})
			cur = &segs[len(segs)-1]
			oldFirst, newFirst = l.old, l.new
			if l.kind == '\\' && i+1 < len(s.lines) {
				oldFirst, newFirst = s.lines[i+1].old, s.lines[i+1].new
			}
		}
		cur.lines = append(cur.lines, l.text)
		switch l.kind {
		case ' ':
			cur.oldCount++
			cur.newCount++
		case '-':
			cur.oldCount++
		case '+':
			cur.newCount++
		}
	}
	if cur != nil {
		cur.finish(oldFirst, newFirst)
	}
	return segs
}

// finish sets the segment's starts from the positions of its first line:
// the first line of a side, or the line after which an empty side applies.
func (sg *segment) finish(oldFirst, newFirst int) {
	sg.oldStart, sg.newStart = oldFirst, newFirst
	if sg.oldCount == 0 {
		sg.oldStart--
	}
	if sg.newCount == 0 {
		sg.newStart--
	}
}
