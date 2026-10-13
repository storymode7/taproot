// / search over the review: which snippet matches, and where in its diff.
package tui

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/storymode7/taproot/core"
)

// searchHit is one match: a snippet (index into Runner.flat) and the rendered
// diff row the match is on. headerHit marks a snippet whose path or symbol
// matched but whose diff did not.
type searchHit struct {
	snip, row int
}

const (
	headerHit = -1
	beforeAll = -2
	afterAll  = math.MaxInt
)

// searchState is the last / search and the hit the cursor was moved to.
type searchState struct {
	re      *regexp.Regexp
	pattern string
	hit     searchHit
}

// compileSearch builds the regexp for pattern with vim smartcase: the match
// ignores case unless the pattern contains an uppercase letter.
func compileSearch(pattern string) (*regexp.Regexp, error) {
	if !strings.ContainsFunc(pattern, unicode.IsUpper) {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

// snippetMatches reports whether re matches the snippet's path, symbol, or
// any raw diff line. It is the cheap pre-check before rendering a diff.
func snippetMatches(re *regexp.Regexp, s *core.Snippet) bool {
	if re.MatchString(s.FilePath) || re.MatchString(s.SymbolPath) {
		return true
	}
	for _, l := range s.DiffLines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// matchingRows returns the indexes of the rendered rows re matches, ignoring
// colour codes.
func matchingRows(re *regexp.Regexp, rows []string) []int {
	var hits []int
	for i, r := range rows {
		if re.MatchString(stripANSI(r)) {
			hits = append(hits, i)
		}
	}
	return hits
}

// nextHit finds the hit after (dir > 0) or before (dir < 0) cur, wrapping
// around n snippets; the search ends back on cur's own snippet so a lone hit
// is found again. hitRows(i) lists snippet i's matching rows in order, with
// headerHit standing in for a snippet whose diff has no matching row.
func nextHit(cur searchHit, dir, n int, hitRows func(i int) []int) (searchHit, bool) {
	for step := 0; step <= n; step++ {
		i := ((cur.snip+dir*step)%n + n) % n
		rows := hitRows(i)
		if dir < 0 {
			for k := len(rows) - 1; k >= 0; k-- {
				if step > 0 || rows[k] < cur.row {
					return searchHit{i, rows[k]}, true
				}
			}
			continue
		}
		for _, r := range rows {
			if step > 0 || r > cur.row {
				return searchHit{i, r}, true
			}
		}
	}
	return searchHit{}, false
}

// promptSearch reads a pattern on the hint line until Enter runs it or
// Esc / Ctrl-C cancels.
func (r *Runner) promptSearch() {
	r.prompting, r.input = true, ""
	for r.prompting {
		r.render()
		switch key := readKey(); key {
		case "", "\033", "\x03":
			r.prompting = false
			r.render()
		case "\r":
			r.prompting = false
			r.startSearch(r.input)
		case "\x7f", "\b":
			if rs := []rune(r.input); len(rs) > 0 {
				r.input = string(rs[:len(rs)-1])
			}
		default:
			if len(key) == 1 && key[0] >= 0x20 {
				r.input += key
			}
		}
	}
}

// startSearch compiles pattern and jumps to its first hit from the cursor.
// An empty pattern repeats the previous search, as in vim.
func (r *Runner) startSearch(pattern string) {
	if pattern != "" {
		re, err := compileSearch(pattern)
		if err != nil {
			r.status = "Bad pattern: " + err.Error()
			r.render()
			return
		}
		r.search = &searchState{re: re, pattern: pattern, hit: searchHit{snip: -1}}
	}
	r.searchJump(1)
}

// searchJump moves to the next (dir > 0) or previous (dir < 0) hit of the
// last search. Hits are counted from the focused snippet, so moving the
// cursor between presses restarts the walk from there.
func (r *Runner) searchJump(dir int) {
	n := r.currentNode()
	if r.search == nil || n == nil {
		r.status = "No previous search"
		r.render()
		return
	}
	cur := searchHit{snip: r.flatIndex(n.Snippet.ID), row: beforeAll}
	if dir < 0 {
		cur.row = afterAll
	}
	if cur.snip == r.search.hit.snip {
		cur.row = r.search.hit.row
	}
	hit, ok := nextHit(cur, dir, len(r.flat), r.hitRows)
	if !ok {
		r.status = "Pattern not found: " + r.search.pattern
		r.render()
		return
	}
	r.search.hit = hit
	r.focusNode(r.flat[hit.snip].ID)
	r.diffScroll = max(hit.row, 0)
	r.render()
}

func (r *Runner) hitRows(i int) []int {
	s := r.flat[i]
	if !snippetMatches(r.search.re, s) {
		return nil
	}
	rows := matchingRows(r.search.re, r.renderedDiff(s, r.cacheCols))
	if len(rows) == 0 {
		return []int{headerHit}
	}
	return rows
}

func (r *Runner) flatIndex(id string) int {
	for i, s := range r.flat {
		if s.ID == id {
			return i
		}
	}
	return -1
}
