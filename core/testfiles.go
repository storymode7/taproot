package core

import "strings"

// isTestPath reports whether a file holds tests, by the naming conventions
// of the supported languages. Tests call the code under review, so they are
// always call-graph roots; the forest moves them after the code instead.
// Storybook stories render the components they show the same way.
func isTestPath(p string) bool {
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	dirs := "/" + p[:len(p)-len(base)]
	switch {
	case strings.HasSuffix(base, "_test.go"),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.HasSuffix(base, "_test.py"),
		base == "conftest.py", base == "tests.py", base == "tests.rs",
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."),
		strings.Contains(base, ".stories."),
		strings.HasSuffix(base, ".java") && (strings.HasPrefix(base, "Test") ||
			strings.HasSuffix(base, "Test.java") || strings.HasSuffix(base, "Tests.java") ||
			strings.HasSuffix(base, "IT.java")),
		strings.Contains(dirs, "/__tests__/"),
		strings.Contains(dirs, "/src/test/"),
		strings.Contains(dirs, "/tests/"), strings.Contains(dirs, "/test/"):
		return true
	}
	return false
}
