package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func TestDefinedGoSymbols(t *testing.T) {
	src := []byte(`package x

func Authenticate(u string) error { return nil }
func (s *Server) Handler() http.Handler { return nil }
func internalOnly() {}
func New() *X { return nil }
type Session struct{}
type lowerType struct{}
func (s *Server) String() string { return "" }
`)
	got := definedGoSymbols(src)
	set := map[string]bool{}
	for _, s := range got {
		set[s] = true
	}
	if !set["Authenticate"] || !set["Handler"] || !set["Session"] {
		t.Fatalf("expected exported decls, got %v", got)
	}
	if set["internalOnly"] || set["lowerType"] {
		t.Fatalf("unexported names should be excluded, got %v", got)
	}
	if set["New"] {
		t.Fatalf("short names should be excluded, got %v", got)
	}
	if set["String"] {
		t.Fatalf("noise method names should be excluded, got %v", got)
	}
}

func TestReferencesSymbol(t *testing.T) {
	src := []byte("x := Authenticate(y)\n call.Handler()\n")
	if !referencesSymbol(src, "Authenticate") {
		t.Fatal("should match a whole-word use")
	}
	if !referencesSymbol(src, "Handler") {
		t.Fatal("should match after a dot")
	}
	if referencesSymbol(src, "Authent") {
		t.Fatal("should not match a partial word")
	}
	if referencesSymbol([]byte("AuthenticateUser()"), "Authenticate") {
		t.Fatal("should not match a longer identifier")
	}
}

func TestSymbolCouplingBothDirections(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("helper.go", "package x\nfunc Authenticate(u string) error { return nil }\n")
	write("caller.go", "package x\nfunc run() { _ = Authenticate(\"a\") }\n")
	write("model.go", "package x\nfunc Frobnicate() {}\n")
	write("user.go", "package x\nfunc go2() { Frobnicate() }\n")

	held := []protocol.Clearance{{Path: "helper.go", Holder: "other"}}
	// caller.go uses Authenticate, which other is editing in helper.go.
	msgs := symbolCoupling("caller.go", dir, held, "me")
	if len(msgs) != 1 || !strings.Contains(msgs[0], "Authenticate") || !strings.Contains(msgs[0], "other") {
		t.Fatalf("expected a coupling note naming Authenticate and other, got %v", msgs)
	}

	// Reverse: editing model.go (defines Frobnicate) while other holds user.go.
	held2 := []protocol.Clearance{{Path: "user.go", Holder: "other"}}
	msgs2 := symbolCoupling("model.go", dir, held2, "me")
	if len(msgs2) != 1 || !strings.Contains(msgs2[0], "Frobnicate") {
		t.Fatalf("expected a reverse-direction coupling note, got %v", msgs2)
	}
}

func TestSymbolCouplingNoFalsePositives(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package x\nfunc Bravo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held := []protocol.Clearance{{Path: "a.go", Holder: "other"}}
	if msgs := symbolCoupling("b.go", dir, held, "me"); len(msgs) != 0 {
		t.Fatalf("unrelated files should not couple, got %v", msgs)
	}
}

func TestSymbolCouplingIgnoresNonGoAndSelf(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\nfunc Authenticate() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-Go edit path yields nothing.
	if msgs := symbolCoupling("README.md", dir, []protocol.Clearance{{Path: "x.go", Holder: "other"}}, "me"); msgs != nil {
		t.Fatalf("non-go edit should yield nil, got %v", msgs)
	}
	// A hold by the caller itself is skipped.
	if err := os.WriteFile(filepath.Join(dir, "y.go"), []byte("package x\nfunc run(){ Authenticate() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msgs := symbolCoupling("y.go", dir, []protocol.Clearance{{Path: "x.go", Holder: "me"}}, "me"); len(msgs) != 0 {
		t.Fatalf("self-held files should be skipped, got %v", msgs)
	}
}

func TestHookPreToolUseSymbolAdvisory(t *testing.T) {
	c, _ := startTower(t)
	t.Setenv("TC_SYMBOLS", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package x\nfunc Authenticate() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package x\nfunc run(){ Authenticate() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Another agent holds helper.go (a different file, so no path overlap).
	if _, err := c.RequestClearance(context.Background(), "other", "helper.go", protocol.ModeAdvisory, "", 0); err != nil {
		t.Fatal(err)
	}
	in := hookInput{SessionID: "symdemo", Cwd: dir, ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"main.go"}`)}
	out := captureStdout(t, func() { hookPreToolUse(in) })
	if !strings.Contains(out, "Authenticate") {
		t.Fatalf("expected a symbol-coupling advisory naming Authenticate, got %q", out)
	}
	// It must be context injection, never a denial.
	if strings.Contains(out, "deny") {
		t.Fatalf("symbol coupling must only warn, got a denial: %q", out)
	}
}

func TestHookPreToolUseSymbolsOffByDefault(t *testing.T) {
	c, _ := startTower(t)
	// TC_SYMBOLS unset: no coupling analysis even with a matching held file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package x\nfunc Authenticate() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package x\nfunc run(){ Authenticate() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RequestClearance(context.Background(), "other", "helper.go", protocol.ModeAdvisory, "", 0); err != nil {
		t.Fatal(err)
	}
	in := hookInput{SessionID: "symoff", Cwd: dir, ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"main.go"}`)}
	out := captureStdout(t, func() { hookPreToolUse(in) })
	if strings.Contains(out, "Authenticate") {
		t.Fatalf("symbol coupling should be off without TC_SYMBOLS=1, got %q", out)
	}
}
