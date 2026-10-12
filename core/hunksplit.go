package core

import (
	"fmt"
	"sort"
	"strings"
)

// splitHunkAtEntities splits a hunk into sub-hunks so that no sub-hunk spans
// more than one entity. Without this, a hunk that straddles two functions
// (context overlap between nearby changes, or a whole new file arriving as
// one hunk) collapses into a single snippet under just one of them —
// violating "never merge two distinct named entities".
//
// Every changed line goes to exactly one piece, the piece of the entity it
// belongs to, and each piece's header carries its exact old and new starts
// and line counts (a side with no lines names the line after which the
// piece applies, 0 for before the first line, as git does). Applying any set
// of pieces to the old file is then well defined, and a piece changes only
// its own entity:
//   - An added line belongs to the post-image entity it falls in, a deleted
//     line to the counterpart of the pre-image entity it falls in (see
//     oldFile); other deleted lines follow assignOwners.
//   - git may pair an unchanged line with an equal line of another entity,
//     such as a function's closing "}" with that of a new function below it.
//     Such a line becomes a deletion in its old entity and an addition in its
//     new one; equal lines at the ends of one entity's change then pair up
//     again, so each entity keeps its own boundary lines.
//   - Within a run of changed lines, each entity's lines sit together where
//     that entity is, so what one piece adds lands in the right place
//     whichever other pieces are applied.
//   - A blank changed line goes with the code it touches.
//   - A "\ No newline at end of file" marker stays with the line it
//     annotates, and the last line of either file stays last.
//
// old is nil when the pre-image is unknown; unchanged lines then keep git's
// pairing. Each piece reports the entity it was cut for (nil outside every
// entity), so callers group pieces by entity identity rather than by name.
// The pairs returned are entities whose changes cannot be staged apart; the
// caller shows each pair as one snippet.
func splitHunkAtEntities(h Hunk, entities []Entity, old *oldFile) ([]piece, [][2]*Entity) {
	body := h.Lines
	if len(body) > 0 && strings.HasPrefix(body[0], "@@") {
		body = body[1:]
	}
	if len(body) == 0 {
		return []piece{{Hunk: h}}, nil
	}

	s := &hunkSplit{entities: entities, old: old, lines: make([]diffLine, len(body))}
	for i, l := range body {
		s.lines[i] = diffLine{text: l, kind: lineKind(l)}
	}
	oldNext, newNext := h.OldStart, h.NewStart
	if h.OldLines == 0 {
		oldNext++
	}
	if h.NewLines == 0 {
		newNext++
	}
	s.number(oldNext, newNext)
	if old != nil {
		s.unpair()
	}
	s.assignOwners()
	s.group()
	s.eofTail()
	s.repair()
	s.number(oldNext, newNext)

	segs := s.segments()
	if len(segs) == 0 {
		return []piece{{Hunk: h}}, nil
	}
	if len(segs) == 1 {
		return []piece{{Hunk: h, entity: segs[0].entity}}, nil
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
	return out, s.merged
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
	split bool // one half of an unchanged line split by unpair
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
	old      *oldFile
	lines    []diffLine
	merged   [][2]*Entity
}

// oldFile is what the split knows of a file's pre-image: its entities, the
// smallest one at each line, and each entity's counterpart among the
// post-image entities (none for an entity that is gone).
type oldFile struct {
	entities []Entity
	byLine   []int
	match    map[*Entity]*Entity
}

// at returns the smallest pre-image entity containing an old line.
func (o *oldFile) at(line int) *Entity {
	if line < 1 || line >= len(o.byLine) || o.byLine[line] < 0 {
		return nil
	}
	return &o.entities[o.byLine[line]]
}

// readOldFile rebuilds a file's pre-image from its post-image and hunks,
// extracts the pre-image entities and pairs them with their counterparts
// among the post-image entities cur. It returns nil when the hunks do not
// fit the post-image.
func readOldFile(post []byte, hunks []Hunk, lang string, cur []Entity) *oldFile {
	pre, ok := preImage(post, hunks)
	if !ok {
		return nil
	}
	text := make([]string, len(pre))
	for i, l := range pre {
		text[i] = l.text
	}
	o := &oldFile{entities: ExtractEntities([]byte(strings.Join(text, "\n")), lang)}
	o.byLine = smallestByLine(o.entities, len(pre))
	o.match = matchEntities(o, cur, pre)
	return o
}

// preLine is one pre-image line: its text, the post-image line it was kept
// as (0 when deleted), and whether that was outside every hunk.
type preLine struct {
	text    string
	kept    int
	outside bool
}

// preImage rebuilds the pre-image from the post-image and the file's hunks.
// ok is false when the hunks do not fit the post-image.
func preImage(post []byte, hunks []Hunk) (pre []preLine, ok bool) {
	cur := splitLines(post)
	if len(cur) > 0 && cur[len(cur)-1] == "" {
		cur = cur[:len(cur)-1]
	}
	next := 1
	keep := func(outside bool) {
		pre = append(pre, preLine{cur[next-1], next, outside})
		next++
	}
	for _, h := range hunks {
		start := h.NewStart
		if h.NewLines == 0 {
			start++
		}
		if start < next || start > len(cur)+1 {
			return nil, false
		}
		for next < start {
			keep(true)
		}
		body := h.Lines
		if len(body) > 0 && strings.HasPrefix(body[0], "@@") {
			body = body[1:]
		}
		for _, l := range body {
			switch k := lineKind(l); k {
			case '-':
				pre = append(pre, preLine{text: strings.TrimSuffix(l[1:], "\r")})
			case ' ', '+':
				if next > len(cur) || cur[next-1] != strings.TrimSuffix(strings.TrimPrefix(l, string(k)), "\r") {
					return nil, false
				}
				if k == ' ' {
					keep(false)
				} else {
					next++
				}
			}
		}
	}
	for next <= len(cur) {
		keep(true)
	}
	return pre, true
}

// smallestByLine returns, for each line 1..n, the index of the smallest
// entity containing it (the first of equal ones), -1 for none.
func smallestByLine(entities []Entity, n int) []int {
	order := make([]int, len(entities))
	for i := range order {
		order[i] = i
	}
	size := func(i int) int { return entities[i].EndLine - entities[i].StartLine }
	sort.Slice(order, func(a, b int) bool {
		if size(order[a]) != size(order[b]) {
			return size(order[a]) > size(order[b])
		}
		return order[a] > order[b]
	})
	by := make([]int, n+1)
	for i := range by {
		by[i] = -1
	}
	for _, i := range order {
		for l := max(entities[i].StartLine, 1); l <= min(entities[i].EndLine, n); l++ {
			by[l] = i
		}
	}
	return by
}

// matchEntities pairs each pre-image entity with its post-image
// counterpart. Lines kept outside every hunk are far from any change and
// cannot be paired otherwise, so an entity whose such lines all fall in one
// post-image entity pairs with it. Of the rest, an entity whose kind and
// name occur once on each side pairs by name; the others pair one to one
// with the post-image entity holding most of their kept non-blank lines, a
// same-named one on a tie, and what is left with the next same-named one in
// order.
func matchEntities(o *oldFile, cur []Entity, pre []preLine) map[*Entity]*Entity {
	old := o.entities
	match := make(map[*Entity]*Entity)
	taken := make([]bool, len(cur))
	pair := func(oi, ci int) {
		match[&old[oi]] = &cur[ci]
		taken[ci] = true
	}

	curBy := smallestByLine(cur, maxEnd(cur))
	shared := make([]map[int]int, len(old))
	fixed := make([]int, len(old)) // the one entity outside lines fall in; -1 none yet, -2 several
	for i := range fixed {
		fixed[i] = -1
	}
	for p, l := range pre {
		if l.kept == 0 || strings.TrimSpace(l.text) == "" {
			continue
		}
		oi, ci := o.byLine[p+1], -1
		if l.kept < len(curBy) {
			ci = curBy[l.kept]
		}
		if oi < 0 {
			continue
		}
		if l.outside {
			switch fixed[oi] {
			case -1:
				if fixed[oi] = ci; ci < 0 {
					fixed[oi] = -2
				}
			case ci:
			default:
				fixed[oi] = -2
			}
		}
		if ci >= 0 {
			if shared[oi] == nil {
				shared[oi] = make(map[int]int)
			}
			shared[oi][ci]++
		}
	}
	for oi := range old {
		if fixed[oi] >= 0 {
			pair(oi, fixed[oi])
		}
	}

	key := func(e Entity) string { return e.Kind + "\x00" + e.Name }
	nOld, nCur := make(map[string]int), make(map[string]int)
	curByKey := make(map[string]int)
	for oi, e := range old {
		if match[&old[oi]] == nil {
			nOld[key(e)]++
		}
	}
	for ci, e := range cur {
		if !taken[ci] {
			nCur[key(e)]++
			curByKey[key(e)] = ci
		}
	}
	for oi, e := range old {
		if k := key(e); match[&old[oi]] == nil && nOld[k] == 1 && nCur[k] == 1 {
			pair(oi, curByKey[k])
		}
	}

	for oi := range old {
		if match[&old[oi]] != nil {
			continue
		}
		cands := make([]int, 0, len(shared[oi]))
		for ci := range shared[oi] {
			cands = append(cands, ci)
		}
		sort.Ints(cands)
		best := -1
		for _, ci := range cands {
			if taken[ci] {
				continue
			}
			n, bn := shared[oi][ci], 0
			if best >= 0 {
				bn = shared[oi][best]
			}
			if best < 0 || n > bn || n == bn && cur[ci].Name == old[oi].Name && cur[best].Name != old[oi].Name {
				best = ci
			}
		}
		if best >= 0 {
			pair(oi, best)
		}
	}
	for oi := range old {
		if match[&old[oi]] != nil {
			continue
		}
		for ci := range cur {
			if !taken[ci] && key(cur[ci]) == key(old[oi]) {
				pair(oi, ci)
				break
			}
		}
	}
	return match
}

func maxEnd(entities []Entity) int {
	m := 0
	for _, e := range entities {
		m = max(m, e.EndLine)
	}
	return m
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

// runEnd returns the end of the run of changed lines starting at b.
func (s *hunkSplit) runEnd(b int) int {
	e := b
	for e < len(s.lines) && s.lines[e].kind != ' ' {
		e++
	}
	return e
}

// unpair splits each unchanged non-blank line whose pre-image entity has
// another counterpart than the post-image entity it falls in into a
// deletion and an addition, each with the marker when both files end on it.
func (s *hunkSplit) unpair() {
	L := s.lines
	out := make([]diffLine, 0, len(L))
	for i := 0; i < len(L); i++ {
		l := L[i]
		if l.kind != ' ' || strings.TrimSpace(content(l)) == "" ||
			s.old.match[s.old.at(l.old)] == s.entityAt(l.new) {
			out = append(out, l)
			continue
		}
		var marker []diffLine
		if i+1 < len(L) && L[i+1].kind == '\\' {
			marker = L[i+1 : i+2]
			i++
		}
		out = append(out, diffLine{text: "-" + content(l), kind: '-', old: l.old, new: l.new, split: true})
		out = append(out, marker...)
		out = append(out, diffLine{text: "+" + content(l), kind: '+', old: l.old + 1, new: l.new, split: true})
		out = append(out, marker...)
	}
	s.lines = out
}

// assignOwners gives every line the entity whose piece it goes to.
func (s *hunkSplit) assignOwners() {
	L := s.lines
	known := make([]bool, len(L))
	for i := range L {
		switch L[i].kind {
		case ' ', '+':
			L[i].owner = s.entityAt(L[i].new)
		case '-':
			if s.old != nil {
				L[i].owner = s.old.match[s.old.at(L[i].old)]
				known[i] = L[i].owner != nil
			}
		}
	}
	for b := 0; b < len(L); {
		if L[b].kind == ' ' {
			b++
			continue
		}
		e := s.runEnd(b)
		s.blankOwners(L[b:e], true)
		b = e
	}
	// A deleted line without a counterpart goes with a deletion it touches
	// that has one, else with the line added right after the deletions (a
	// replacement stays together), else with the line before it. Deleted
	// blank lines alone go where a blank line there would. All lines of an
	// entity that is gone go to one piece.
	gone := make(map[*Entity]*Entity)
	for i := 0; i < len(L); {
		if L[i].kind != '-' || known[i] {
			i++
			continue
		}
		j := i
		for j < len(L) && (L[j].kind == '-' && !known[j] || L[j].kind == '\\') {
			j++
		}
		next := j
		for next < len(L) && (L[next].kind == '-' || L[next].kind == '\\') {
			next++
		}
		var owner *Entity
		switch {
		case j < len(L) && L[j].kind == '-':
			owner = L[j].owner
		case i > 0 && L[i-1].kind == '-':
			owner = L[i-1].owner
		case next < len(L) && L[next].kind == '+':
			owner = L[next].owner
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
			if s.old == nil || L[k].kind != '-' {
				continue
			}
			if oe := s.old.at(L[k].old); oe != nil {
				if o, ok := gone[oe]; ok {
					L[k].owner = o
				} else {
					gone[oe] = owner
				}
			}
		}
		i = j
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
// the entity it leads into. Before repair, halves of a split unchanged line
// do not count (skipSplit): they may pair up again.
func (s *hunkSplit) blankOwners(run []diffLine, skipSplit bool) {
	var prev *Entity
	seen := false
	for i := range run {
		if run[i].kind != '+' || skipSplit && run[i].split {
			continue
		}
		if !blank(run[i]) {
			prev, seen = run[i].owner, true
			continue
		}
		next := -1
		for j := i + 1; j < len(run); j++ {
			if run[j].kind == '+' && !(skipSplit && run[j].split) && !blank(run[j]) {
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

// units cuts a run of changed lines into lines, each with its marker.
func units(run []diffLine) [][]diffLine {
	var out [][]diffLine
	for i := 0; i < len(run); i++ {
		if i+1 < len(run) && run[i+1].kind == '\\' {
			out = append(out, run[i:i+2])
			i++
			continue
		}
		out = append(out, run[i:i+1])
	}
	return out
}

// group reorders each run of changed lines so that each owner's lines sit
// together, deletions first. Owners keep the order their deletions and
// their additions come in; otherwise they go in the order of their first
// added line (of where their entity starts when they only delete): what a
// piece adds then lands where its entity is, whichever other pieces are
// applied. The owners of a run whose deletions or additions interleave
// cannot be staged apart; they merge.
func (s *hunkSplit) group() {
	for b := 0; b < len(s.lines); {
		if s.lines[b].kind == ' ' {
			b++
			continue
		}
		e := s.runEnd(b)
		if owners := groupRun(s.lines[b:e]); owners != nil {
			for _, o := range owners[1:] {
				s.merged = append(s.merged, [2]*Entity{owners[0], o})
			}
		}
		b = e
	}
}

// groupRun groups one run. It returns the run's owners when they
// interleave, leaving the run as it is.
func groupRun(run []diffLine) []*Entity {
	us := units(run)
	idx := make(map[*Entity]int)
	var owners []*Entity
	type place struct{ pos, first int }
	var at []place
	adds := make(map[*Entity]bool)
	for k, u := range us {
		o := u[0].owner
		i, seen := idx[o]
		if !seen {
			i = len(owners)
			idx[o] = i
			owners = append(owners, o)
			p := place{u[0].new, k}
			if o != nil {
				p.pos = o.StartLine
			}
			at = append(at, p)
		}
		if u[0].kind == '+' && !adds[o] {
			at[i].pos, adds[o] = u[0].new, true
		}
	}
	if len(owners) < 2 {
		return nil
	}
	before := make([]map[int]bool, len(owners))
	indeg := make([]int, len(owners))
	last := map[byte]int{'-': -1, '+': -1}
	for _, u := range us {
		k, o := u[0].kind, idx[u[0].owner]
		if p := last[k]; p >= 0 && p != o {
			if before[p] == nil {
				before[p] = make(map[int]bool)
			}
			if !before[p][o] {
				before[p][o] = true
				indeg[o]++
			}
		}
		last[k] = o
	}
	var order []int
	done := make([]bool, len(owners))
	for len(order) < len(owners) {
		next := -1
		for i := range owners {
			if done[i] || indeg[i] > 0 {
				continue
			}
			if next < 0 || at[i].pos < at[next].pos || at[i].pos == at[next].pos && at[i].first < at[next].first {
				next = i
			}
		}
		if next < 0 {
			return owners
		}
		done[next] = true
		order = append(order, next)
		for o := range before[next] {
			indeg[o]--
		}
	}
	out := make([]diffLine, 0, len(run))
	for _, i := range order {
		for _, kind := range []byte{'-', '+'} {
			for _, u := range us {
				if u[0].kind == kind && idx[u[0].owner] == i {
					out = append(out, u...)
				}
			}
		}
	}
	copy(run, out)
	return nil
}

// eofTail keeps a file's last line at its end when either side lacks the
// final newline.
//
// When the new file ends without one, the piece adding its last line keeps
// it last only together with the pieces deleting old lines after it: those
// merge with it.
//
// When the old file ends without one, its last line is deleted ("-X" then
// the marker), and code added after it applies only together with that
// line's newline fix. When the line's owner adds it back unchanged as the
// last line it adds before code of another piece, the fix ("-X", marker,
// "+X") moves to that piece, so a function appended at the end carries the
// newline it needs; otherwise the code's owner merges with the line's owner.
// Every owner of code added after the old last line ends up in one snippet
// either way.
func (s *hunkSplit) eofTail() {
	s.newLastLine()
	L := s.lines
	x := -1
	for i := 0; i+1 < len(L); i++ {
		if L[i].kind == '-' && L[i+1].kind == '\\' {
			x = i
			break
		}
	}
	if x < 0 {
		return
	}
	own := L[x].owner
	d := -1
	for i := x + 2; i < len(L); i++ {
		if L[i].kind == '+' && L[i].owner != own {
			d = i
			break
		}
	}
	if d < 0 {
		return
	}
	r := -1
	for i := x + 2; i < d; i++ {
		if L[i].kind == '+' && L[i].owner == own {
			r = i
		}
	}
	if r >= 0 && content(L[r]) == content(L[x]) && L[r+1].kind != '\\' {
		fix := []diffLine{L[x], L[x+1]}
		copy(L[x:], L[x+2:r])
		copy(L[r-2:], fix)
		own = L[d].owner
		for i := r - 2; i <= r; i++ {
			L[i].owner = own
		}
	}
	for i := d; i < len(L); i++ {
		if L[i].owner != own {
			s.merged = append(s.merged, [2]*Entity{own, L[i].owner})
		}
	}
}

// newLastLine merges the piece that adds the new file's last line, a line
// without a newline, with each piece deleting old lines after its additions.
func (s *hunkSplit) newLastLine() {
	L := s.lines
	y := -1
	for i := 0; i+1 < len(L); i++ {
		if L[i].kind == '+' && L[i+1].kind == '\\' {
			y = i
		}
	}
	if y < 0 {
		return
	}
	q := L[y].owner
	first := y
	for i := y; i > 0 && L[i-1].kind != ' '; i-- {
		if L[i-1].kind == '+' && L[i-1].owner == q {
			first = i - 1
		}
	}
	for _, l := range L[first:] {
		if l.kind == '-' && l.owner != q {
			s.merged = append(s.merged, [2]*Entity{q, l.owner})
		}
	}
}

// repair pairs equal lines at the start and at the end of one owner's
// change again, a deletion with an addition, so a piece keeps its unchanged
// boundary lines as context rather than deleting and re-adding them. Lines
// are equal when their text is and both end in a newline or neither does.
func (s *hunkSplit) repair() {
	L := s.lines
	out := make([]diffLine, 0, len(L))
	for b := 0; b < len(L); {
		if L[b].kind == ' ' {
			out = append(out, L[b])
			b++
			continue
		}
		e := b
		for e < len(L) && L[e].kind != ' ' && L[e].owner == L[b].owner {
			e++
		}
		out = append(out, repairBlock(L[b:e])...)
		b = e
	}
	s.lines = out
	// A blank line that went with a line now paired again goes with what is
	// still changed around it.
	for b := 0; b < len(out); {
		if out[b].kind == ' ' {
			b++
			continue
		}
		e := s.runEnd(b)
		s.blankOwners(out[b:e], false)
		b = e
	}
}

func repairBlock(block []diffLine) []diffLine {
	var dels, adds [][]diffLine
	for _, u := range units(block) {
		switch u[0].kind {
		case '-':
			if len(adds) > 0 {
				return block
			}
			dels = append(dels, u)
		case '+':
			adds = append(adds, u)
		}
	}
	same := func(a, b []diffLine) bool { return content(a[0]) == content(b[0]) && len(a) == len(b) }
	k := 0
	for k < len(dels) && k < len(adds) && same(dels[k], adds[k]) {
		k++
	}
	m := 0
	for m < len(dels)-k && m < len(adds)-k && same(dels[len(dels)-1-m], adds[len(adds)-1-m]) {
		m++
	}
	if k == 0 && m == 0 {
		return block
	}
	keep := func(u []diffLine) []diffLine {
		c := append([]diffLine(nil), u...)
		c[0].kind, c[0].text = ' ', " "+content(u[0])
		return c
	}
	out := make([]diffLine, 0, len(block))
	for _, u := range dels[:k] {
		out = append(out, keep(u)...)
	}
	for _, u := range dels[k : len(dels)-m] {
		out = append(out, u...)
	}
	for _, u := range adds[k : len(adds)-m] {
		out = append(out, u...)
	}
	for _, u := range dels[len(dels)-m:] {
		out = append(out, keep(u)...)
	}
	return out
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
