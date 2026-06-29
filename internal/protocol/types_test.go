package protocol

import (
	"runtime"
	"testing"
)

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"a.go", "a.go", true},
		{"./a.go", "a.go", true},
		{"src/a.go", "src/b.go", false},
		{"internal/", "internal/api/server.go", true},
		{"internal", "internal/api/server.go", true},
		{"internal/api/*.go", "internal/api/server.go", true},
		{"internal/*.go", "internal/api/server.go", false}, // single * stays in one segment
		{"internal/**", "internal/api/server.go", true},    // ** crosses separators
		{"internal/**", "internal", true},                  // ** also matches zero segments
		{"src/**/*.go", "src/a/b/c.go", true},
		{"src/**/*.go", "src/a/b/c.txt", false},
		{"src/**", "lib/a.go", false},
		{"cmd/tc", "cmd/tc/main.go", true}, // holding the dir covers files under it
		{"src/a.go", "lib/a.go", false},
		{"app/[id].tsx", "app/x.tsx", false}, // brackets are literal, not a glob class
		{"app/[id].tsx", "app/[id].tsx", true},
		{"", "a.go", false},
	}
	for _, c := range cases {
		if got := PathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("PathsOverlap(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestPathsOverlapCaseFold verifies the comparison folds case only where the
// filesystem does, so the test asserts platform-correct behaviour rather than a
// fixed answer.
func TestPathsOverlapCaseFold(t *testing.T) {
	got := PathsOverlap("src/App.go", "src/app.go")
	want := caseInsensitiveFS
	if got != want {
		t.Errorf("PathsOverlap case fold on %s: got %v, want %v", runtime.GOOS, got, want)
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"./a/b.go":  "a/b.go",
		"a/../b.go": "b.go",
		"src/":      "src/",
		"  x.go  ":  "x.go",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
	// Backslash is a separator only on Windows; on Unix it is a legal filename
	// character and must survive normalization untouched.
	got := NormalizePath("a\\b.go")
	want := "a\\b.go"
	if runtime.GOOS == "windows" {
		want = "a/b.go"
	}
	if got != want {
		t.Errorf("NormalizePath(%q) on %s = %q, want %q", "a\\b.go", runtime.GOOS, got, want)
	}
}
