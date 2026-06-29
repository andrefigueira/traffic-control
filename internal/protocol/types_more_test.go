package protocol

import "testing"

func TestNormalizePathEmptyAndWhitespace(t *testing.T) {
	if got := NormalizePath(""); got != "" {
		t.Fatalf("empty stays empty, got %q", got)
	}
	if got := NormalizePath("   "); got != "" {
		t.Fatalf("all-whitespace normalizes to empty, got %q", got)
	}
}

func TestPathsOverlapEmptyNeverMatches(t *testing.T) {
	if PathsOverlap("", "") {
		t.Fatal("two empty paths must not overlap")
	}
	if PathsOverlap("", "a.go") || PathsOverlap("a.go", "") {
		t.Fatal("an empty path must never overlap anything")
	}
}

// TestMatchGlobEdges drives matchSegments through branches the main table does
// not: a bare ** matching anything, a pattern longer than the name, and a
// literal segment mismatch.
func TestMatchGlobEdges(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"**", "a/b/c.go", true},      // trailing ** swallows everything
		{"a/**/b.go", "a/b.go", true}, // ** matches zero intermediate segments
		{"a/*/c/d", "a/b/c", false},   // pattern longer than the candidate
		{"a/x*.go", "a/y.go", false},  // glob segment that cannot match
		{"a/*/c", "a/b/c", true},      // single * inside one segment
		{"a/*/c", "a/b/d/c", false},   // single * does not cross separators
	}
	for _, c := range cases {
		if got := PathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("PathsOverlap(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestDirAncestorBothDirections(t *testing.T) {
	// A directory hold covers a child regardless of which side it appears on.
	if !PathsOverlap("cmd/tc", "cmd/tc/main.go") {
		t.Fatal("parent dir should cover its child")
	}
	if !PathsOverlap("cmd/tc/main.go", "cmd/tc") {
		t.Fatal("child should overlap its parent dir")
	}
	if PathsOverlap("cmd/tc", "cmd/tcother/x.go") {
		t.Fatal("a sibling sharing a prefix is not a child")
	}
}
