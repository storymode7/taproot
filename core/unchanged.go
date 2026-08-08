// Unchanged-caller annotations.
//
// A changed entity whose only caller is unchanged code shows up as a
// context-free root in the review forest. We hold the full post-image Source
// of every changed file, so callers living in unchanged regions of those
// files can still be found: extract each file's entities, keep the ones no
// snippet covers, and scan their bodies with the same call-site scanner
// callscan.go uses. Matches annotate the changed snippet rather than adding
// edges — unchanged code is context, not review material.
package core

import "sort"

// AnnotateUnchangedCallers scans the unchanged entities of each changed file
// for call sites naming a changed snippet and records them in the target
// snippet's UnchangedCallers as "path::Entity" (deduped, sorted).
//
// Only snippets of kind function/class/type are annotation targets, resolved
// by last dotted segment with the same same-file/same-dir preference as
// BuildCallEdges. Lines of an unchanged entity that fall inside a nested
// entity (e.g. methods inside a class span) are skipped so containers don't
// double-count their members' calls.
func AnnotateUnchangedCallers(files []*FileDiff, snippets []*Snippet) {
	// Call-site targets: last dotted segment → changed snippets declaring it.
	table := make(map[string][]*Snippet)
	// Changed symbol paths per file, to tell unchanged entities apart.
	changed := make(map[string]map[string]bool)
	for _, s := range snippets {
		if changed[s.FilePath] == nil {
			changed[s.FilePath] = make(map[string]bool)
		}
		changed[s.FilePath][s.SymbolPath] = true
		name := lastSegment(s.SymbolPath)
		if len(name) < 2 {
			continue
		}
		switch s.NodeType {
		case "function", "class", "type":
			table[name] = append(table[name], s)
		}
	}
	if len(table) == 0 {
		return
	}

	seen := make(map[string]map[string]bool) // snippet ID → caller labels

	for _, f := range files {
		if f.IsDeleted || f.IsBinary || len(f.Source) == 0 || f.Language == "" {
			continue
		}
		entities := f.Entities
		if len(entities) == 0 {
			entities = ExtractEntities(f.Source, f.Language)
		}
		if len(entities) == 0 {
			continue
		}
		lines := splitLines(f.Source)
		// Same-file/same-dir disambiguation reuses resolveTargets; only the
		// caller's FilePath matters to it.
		from := &Snippet{FilePath: f.Path}

		for i := range entities {
			e := &entities[i]
			if e.Kind == "imports" || changed[f.Path][e.Name] {
				continue
			}
			nested := entityNestedSpans(entities, i)
			for j := e.StartLine; j <= e.EndLine && j <= len(lines); j++ {
				if inSpans(j, nested) {
					continue
				}
				for _, name := range callSites(lines[j-1], f.Language) {
					targets, ok := table[name]
					if !ok {
						continue
					}
					for _, t := range resolveTargets(from, targets) {
						if seen[t.ID] == nil {
							seen[t.ID] = make(map[string]bool)
						}
						label := f.Path + "::" + e.Name
						if !seen[t.ID][label] {
							seen[t.ID][label] = true
							t.UnchangedCallers = append(t.UnchangedCallers, label)
						}
					}
				}
			}
		}
	}

	for _, s := range snippets {
		sort.Strings(s.UnchangedCallers)
	}
}

// entityNestedSpans returns the line spans of entities strictly nested
// inside entities[idx]'s span — the entity-list analogue of nestedSpans.
func entityNestedSpans(entities []Entity, idx int) [][2]int {
	e := &entities[idx]
	var spans [][2]int
	for i := range entities {
		t := &entities[i]
		if i == idx {
			continue
		}
		if t.StartLine >= e.StartLine && t.EndLine <= e.EndLine &&
			(t.EndLine-t.StartLine) < (e.EndLine-e.StartLine) {
			spans = append(spans, [2]int{t.StartLine, t.EndLine})
		}
	}
	return spans
}
