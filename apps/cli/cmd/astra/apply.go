package main

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/storymode7/astra/core"
)

// fragment is one @@-headed run of a snippet's diff lines: the old lines
// it replaces and the new lines it puts in their place, each with its
// newline unless a "\ No newline at end of file" marker follows it.
type fragment struct {
	oldStart, oldCount, newStart int
	pre, post                    [][]byte
}

// begin is the index of the first old line the fragment replaces; a
// fragment with no old lines goes after old line oldStart, as in git.
func (f fragment) begin() int {
	if f.oldCount == 0 {
		return f.oldStart
	}
	return f.oldStart - 1
}

var reHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// fragments cuts a snippet's diff lines at their @@ headers. The counts in
// each header must match the lines under it.
func fragments(s *core.Snippet) ([]fragment, error) {
	var out []fragment
	var newCount int
	check := func() error {
		if len(out) == 0 {
			return nil
		}
		f := out[len(out)-1]
		if len(f.pre) != f.oldCount || len(f.post) != newCount {
			return fmt.Errorf("%s: @@ -%d,%d +%d,%d @@ has %d old and %d new lines",
				s.FilePath, f.oldStart, f.oldCount, f.newStart, newCount, len(f.pre), len(f.post))
		}
		return nil
	}
	for i, l := range s.DiffLines {
		if m := reHunkHeader.FindStringSubmatch(l); m != nil {
			if err := check(); err != nil {
				return nil, err
			}
			f := fragment{oldCount: 1}
			newCount = 1
			f.oldStart, _ = strconv.Atoi(m[1])
			if m[2] != "" {
				f.oldCount, _ = strconv.Atoi(m[2])
			}
			f.newStart, _ = strconv.Atoi(m[3])
			if m[4] != "" {
				newCount, _ = strconv.Atoi(m[4])
			}
			out = append(out, f)
			continue
		}
		if strings.HasPrefix(l, `\`) {
			continue
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s: diff line %q before any @@ header", s.FilePath, l)
		}
		f := &out[len(out)-1]
		text := []byte(l)
		if len(text) > 0 {
			text = text[1:]
		}
		if i+1 >= len(s.DiffLines) || !strings.HasPrefix(s.DiffLines[i+1], `\`) {
			text = append(text, '\n')
		}
		switch {
		case strings.HasPrefix(l, "-"):
			f.pre = append(f.pre, text)
		case strings.HasPrefix(l, "+"):
			f.post = append(f.post, text)
		default:
			f.pre = append(f.pre, text)
			f.post = append(f.post, text)
		}
	}
	if err := check(); err != nil {
		return nil, err
	}
	return out, nil
}

// applyFragments puts fragments into base, each at its exact old line:
// the text git diff read as the pre-image. Their old lines must be in base
// exactly where they say, and no two may claim the same old line; nothing
// is searched for, so the result does not depend on which other fragments
// are applied or in what order.
func applyFragments(base []byte, frags []fragment) ([]byte, error) {
	frags = append([]fragment(nil), frags...)
	sort.SliceStable(frags, func(i, j int) bool {
		a, b := frags[i], frags[j]
		if a.begin() != b.begin() {
			return a.begin() < b.begin()
		}
		// An insertion at a line goes before a change that starts there.
		if (a.oldCount == 0) != (b.oldCount == 0) {
			return a.oldCount == 0
		}
		return a.newStart < b.newStart
	})
	lines := splitLines(base)
	var out bytes.Buffer
	cur := 0
	for _, f := range frags {
		b := f.begin()
		if b < cur || b+f.oldCount > len(lines) {
			return nil, fmt.Errorf("@@ -%d,%d @@ overlaps another change or runs past the end", f.oldStart, f.oldCount)
		}
		for _, l := range lines[cur:b] {
			out.Write(l)
		}
		for k, want := range f.pre {
			if !bytes.Equal(lines[b+k], want) {
				return nil, fmt.Errorf("@@ -%d,%d @@ does not match line %d", f.oldStart, f.oldCount, b+k+1)
			}
		}
		for _, l := range f.post {
			out.Write(l)
		}
		cur = b + f.oldCount
	}
	for _, l := range lines[cur:] {
		out.Write(l)
	}
	return out.Bytes(), nil
}

// splitLines cuts text into lines that keep their newline; the last one
// lacks it when the text does not end in one.
func splitLines(text []byte) [][]byte {
	var out [][]byte
	for len(text) > 0 {
		i := bytes.IndexByte(text, '\n')
		if i < 0 {
			out = append(out, text)
			break
		}
		out = append(out, text[:i+1])
		text = text[i+1:]
	}
	return out
}
