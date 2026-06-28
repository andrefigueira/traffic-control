package protocol

import "testing"

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
		{"internal/*.go", "internal/api/server.go", false},
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

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"./a/b.go":  "a/b.go",
		"a/../b.go": "b.go",
		"src/":      "src/",
		"a\\b.go":   "a/b.go",
		"  x.go  ":  "x.go",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}
