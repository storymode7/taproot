package tui

import (
	"reflect"
	"testing"

	"github.com/storymode7/astra/core"
)

func fnSnippet(ret string) *core.Snippet {
	return &core.Snippet{ID: "alpha", FilePath: "a.go", SymbolPath: "alpha", NodeType: "function", StartLine: 3,
		DiffLines: []string{"@@ -3,3 +3,3 @@", " func alpha() int {", "-\treturn 1", "+\treturn " + ret, " }"}}
}

// TestSetStatus_ReloadsWhenTheDiffChanged: when the files or the index
// changed since the diff was read, y stages nothing and records nothing;
// the diff is read again, and the next y stages what is there now.
func TestSetStatus_ReloadsWhenTheDiffChanged(t *testing.T) {
	stale, fresh := fnSnippet("10"), fnSnippet("11")
	r := testRunner(t, stale, manySnippets(1, 2)[0])
	r.applyMode = true
	var staged []string
	r.stage = func(s *core.Snippet, reverse bool) error {
		staged = append(staged, s.DiffLines[3])
		return nil
	}
	why := "a.go changed on disk"
	r.Changed = func() string {
		w := why
		why = ""
		return w
	}
	r.Reload = func() (*core.ReviewForest, []core.SkippedFile, error) {
		return core.BuildForest([]*core.Snippet{manySnippets(1, 2)[0], fresh}, nil), nil, nil
	}
	fixedSize(t, 24, 80)

	captureStdout(t, func() { r.setStatus("accepted") })
	if len(staged) != 0 {
		t.Errorf("y staged %q from the diff read before the change", staged)
	}
	if want := "a.go changed on disk outside astra: reloaded the diff, verdict not recorded"; r.status != want {
		t.Errorf("status = %q, want %q", r.status, want)
	}
	if n := r.currentNode(); n == nil || n.Snippet != fresh {
		t.Errorf("the cursor is not on the reloaded alpha: %v", n)
	}
	if got := r.store.Get(stale.ID).Status; got != "pending" {
		t.Errorf("the stale change was recorded as %q", got)
	}

	captureStdout(t, func() { r.setStatus("accepted") })
	if !reflect.DeepEqual(staged, []string{"+\treturn 11"}) || r.store.Get(fresh.ID).Status != "accepted" {
		t.Errorf("second y staged %q, status %q; want the new change accepted", staged, r.store.Get(fresh.ID).Status)
	}
}
