package core

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite parity golden files")

// TestParityFixtures runs each testdata/parity/*.json request through
// ProcessJSON and compares against its .golden.json. The same fixtures are
// fed to the WASM build by scripts/parity-check.mjs — one source of truth
// for "the browser gets identical results".
func TestParityFixtures(t *testing.T) {
	reqs, err := filepath.Glob("testdata/parity/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, reqPath := range reqs {
		if strings.HasSuffix(reqPath, ".golden.json") {
			continue
		}
		t.Run(filepath.Base(reqPath), func(t *testing.T) {
			reqJSON, err := os.ReadFile(reqPath)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ProcessJSON(string(reqJSON))
			if err != nil {
				t.Fatalf("ProcessJSON: %v", err)
			}
			// Normalize for a readable golden file.
			var buf bytes.Buffer
			if err := json.Indent(&buf, []byte(got), "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty := buf.String() + "\n"

			goldenPath := strings.TrimSuffix(reqPath, ".json") + ".golden.json"
			if *updateGolden {
				if err := os.WriteFile(goldenPath, []byte(pretty), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("missing golden (run: go test ./core -run Parity -update): %v", err)
			}
			if pretty != string(want) {
				t.Errorf("response differs from golden %s\ngot:\n%s", goldenPath, pretty)
			}
		})
	}
}
