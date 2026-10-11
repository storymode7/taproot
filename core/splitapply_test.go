package core

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// fragment is one @@ fragment of a snippet's DiffLines.
type fragment struct {
	oldStart, oldCount, newStart, newCount int
	body                                   []string
}

var reFragHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// fragments cuts diff lines at their @@ headers and checks that each
// header's counts match its body and that no fragment opens with a marker.
func fragments(lines []string) ([]fragment, error) {
	var out []fragment
	for _, l := range lines {
		if m := reFragHeader.FindStringSubmatch(l); m != nil {
			f := fragment{oldCount: 1, newCount: 1}
			f.oldStart, _ = strconv.Atoi(m[1])
			if m[2] != "" {
				f.oldCount, _ = strconv.Atoi(m[2])
			}
			f.newStart, _ = strconv.Atoi(m[3])
			if m[4] != "" {
				f.newCount, _ = strconv.Atoi(m[4])
			}
			out = append(out, f)
			continue
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("diff line %q before any @@ header", l)
		}
		out[len(out)-1].body = append(out[len(out)-1].body, l)
	}
	for _, f := range out {
		pre, post := f.sides()
		if len(pre) != f.oldCount || len(post) != f.newCount {
			return nil, fmt.Errorf("@@ -%d,%d +%d,%d @@ has %d old and %d new lines: %q",
				f.oldStart, f.oldCount, f.newStart, f.newCount, len(pre), len(post), f.body)
		}
		if len(f.body) > 0 && strings.HasPrefix(f.body[0], `\`) {
			return nil, fmt.Errorf("fragment opens with a marker: %q", f.body)
		}
	}
	return out, nil
}

// sides returns the old and new lines of a fragment, each with its newline
// unless a "\ No newline at end of file" marker follows it.
func (f fragment) sides() (pre, post []string) {
	for i, l := range f.body {
		if strings.HasPrefix(l, `\`) {
			continue
		}
		nl := "\n"
		if i+1 < len(f.body) && strings.HasPrefix(f.body[i+1], `\`) {
			nl = ""
		}
		text := ""
		if l != "" {
			text = l[1:]
		}
		switch {
		case strings.HasPrefix(l, "-"):
			pre = append(pre, text+nl)
		case strings.HasPrefix(l, "+"):
			post = append(post, text+nl)
		default:
			pre = append(pre, text+nl)
			post = append(post, text+nl)
		}
	}
	return pre, post
}

// begin is the 0-based index of the first old line the fragment replaces;
// a fragment with no old lines applies after old line oldStart.
func (f fragment) begin() int {
	if f.oldCount == 0 {
		return f.oldStart
	}
	return f.oldStart - 1
}

// textLines splits text into lines that keep their newline; the last one
// lacks it when the text does not end in a newline.
func textLines(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// applyFragments applies fragments to old at their exact old-side
// positions, in old-position order. It fails when two fragments claim the
// same old line or a fragment's old lines differ from old. With exactNew,
// each fragment must also land at its new-side position.
func applyFragments(old string, frags []fragment, exactNew bool) (string, error) {
	frags = append([]fragment(nil), frags...)
	sort.SliceStable(frags, func(i, j int) bool {
		if frags[i].begin() != frags[j].begin() {
			return frags[i].begin() < frags[j].begin()
		}
		return frags[i].newStart < frags[j].newStart
	})
	src := textLines(old)
	var out []string
	cur := 0
	for _, f := range frags {
		b := f.begin()
		if b < cur || b+f.oldCount > len(src) {
			return "", fmt.Errorf("@@ -%d,%d @@ overlaps another fragment or runs past the old file", f.oldStart, f.oldCount)
		}
		out = append(out, src[cur:b]...)
		pre, post := f.sides()
		if got := strings.Join(src[b:b+f.oldCount], ""); got != strings.Join(pre, "") {
			return "", fmt.Errorf("@@ -%d,%d @@ old lines %q, old file has %q", f.oldStart, f.oldCount, pre, got)
		}
		if exactNew {
			at := f.newStart - 1
			if f.newCount == 0 {
				at = f.newStart
			}
			if at != len(out) {
				return "", fmt.Errorf("@@ +%d,%d @@ lands at new line %d", f.newStart, f.newCount, len(out)+1)
			}
		}
		out = append(out, post...)
		cur = b + f.oldCount
	}
	out = append(out, src[cur:]...)
	return strings.Join(out, ""), nil
}

// snippetFragments collects the fragments of the given snippets.
func snippetFragments(t *testing.T, snippets ...*Snippet) []fragment {
	t.Helper()
	var all []fragment
	for _, s := range snippets {
		frags, err := fragments(s.DiffLines)
		if err != nil {
			t.Fatalf("%s %q: %v", s.FilePath, s.SymbolPath, err)
		}
		all = append(all, frags...)
	}
	return all
}

// checkStaging checks that the snippets of one file reproduce the new text
// exactly when all are applied, and that each one applies alone with the
// result want[symbol] (when given).
func checkStaging(t *testing.T, oldText, newText string, snippets []*Snippet, want map[string]string) {
	t.Helper()
	got, err := applyFragments(oldText, snippetFragments(t, snippets...), true)
	if err != nil {
		t.Fatalf("applying every snippet: %v", err)
	}
	if got != newText {
		t.Errorf("applying every snippet gives\n%q\nwant\n%q", got, newText)
	}
	for _, s := range snippets {
		alone, err := applyFragments(oldText, snippetFragments(t, s), false)
		if err != nil {
			t.Errorf("applying %q alone: %v", s.SymbolPath, err)
			continue
		}
		if w, ok := want[s.SymbolPath]; ok && alone != w {
			t.Errorf("applying %q alone gives\n%q\nwant\n%q", s.SymbolPath, alone, w)
		}
	}
}
