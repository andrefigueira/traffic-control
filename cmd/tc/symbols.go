package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// Semantic-coupling awareness. Path clearance catches two agents reaching for
// the same file; it cannot see that one agent is changing a function another
// agent calls from a different file. This is a heuristic, zero-dependency,
// opt-in (TC_SYMBOLS=1) advisory that closes part of that gap: when the file
// about to be edited and a file another agent holds share an exported symbol
// that one defines and the other uses, the agent is told. It is regex over
// source rather than a real parser, so it favours the common cases and accepts
// the odd miss, it compares only files of the same language, and it only ever
// warns, never blocks. Go, TypeScript/JavaScript, and Python are recognized.

var (
	goFuncDecl = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Z]\w*)\s*\(`)
	goTypeDecl = regexp.MustCompile(`(?m)^type\s+([A-Z]\w*)`)

	tsFuncDecl  = regexp.MustCompile(`(?m)^\s*export\s+(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)
	tsClassDecl = regexp.MustCompile(`(?m)^\s*export\s+(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)
	tsVarDecl   = regexp.MustCompile(`(?m)^\s*export\s+(?:const|let|var)\s+([A-Za-z_$][\w$]*)`)
	tsTypeDecl  = regexp.MustCompile(`(?m)^\s*export\s+(?:type|interface|enum)\s+([A-Za-z_$][\w$]*)`)

	pyDefDecl   = regexp.MustCompile(`(?m)^def\s+([A-Za-z_]\w*)`)
	pyClassDecl = regexp.MustCompile(`(?m)^class\s+([A-Za-z_]\w*)`)
)

// noiseSymbols are names so common (interface methods, ubiquitous helpers) that
// coupling on them is almost always a false positive.
var noiseSymbols = map[string]bool{
	"String": true, "Error": true, "Read": true, "Write": true, "Close": true,
	"Len": true, "Less": true, "Swap": true, "ServeHTTP": true,
	"MarshalJSON": true, "UnmarshalJSON": true, "Bytes": true,
	"render": true, "default": true, "handler": true, "main": true,
}

// langOf maps a path to a language key, or "" if it is not a recognized source
// file. Coupling is only ever compared within one language key.
func langOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return "ts"
	case ".py":
		return "py"
	default:
		return ""
	}
}

// definedSymbols extracts the exported, coupling-worthy declarations from a
// source file, dispatching on its language.
func definedSymbols(path string, src []byte) []string {
	switch langOf(path) {
	case "go":
		return extractSymbols(src, goFuncDecl, goTypeDecl)
	case "ts":
		return extractSymbols(src, tsFuncDecl, tsClassDecl, tsVarDecl, tsTypeDecl)
	case "py":
		return extractSymbols(src, pyDefDecl, pyClassDecl)
	default:
		return nil
	}
}

// definedGoSymbols is retained as the Go-specific extractor.
func definedGoSymbols(src []byte) []string {
	return extractSymbols(src, goFuncDecl, goTypeDecl)
}

// extractSymbols runs the given declaration patterns over src and returns the
// distinct capture-1 names, dropping very short names, underscore-private names,
// and the noise list to keep false positives down.
func extractSymbols(src []byte, patterns ...*regexp.Regexp) []string {
	seen := map[string]bool{}
	var out []string
	for _, re := range patterns {
		for _, m := range re.FindAllSubmatch(src, -1) {
			s := string(m[1])
			if len(s) < 4 || s[0] == '_' || seen[s] || noiseSymbols[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// referencesSymbol reports whether sym appears as a whole word in src, so "Foo"
// matches a call to Foo but not FooBar or BarFoo.
func referencesSymbol(src []byte, sym string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(sym) + `\b`)
	return re.Match(src)
}

// symbolCoupling returns advisory lines describing semantic coupling between the
// file about to be edited and the files other agents currently hold, comparing
// only files of the same language. It reads from disk, so it is best-effort: a
// missing file, an unrecognized path, or a new file with nothing on disk yet
// simply yields fewer or no lines. Capped so a busy tree cannot produce a wall
// of warnings.
func symbolCoupling(editPath, cwd, workspace string, held []protocol.Clearance, callsign string) []string {
	editLang := langOf(editPath)
	if editLang == "" {
		return nil
	}
	editSrc, _ := os.ReadFile(absUnder(editPath, cwd)) // empty for a not-yet-created file
	editDefs := definedSymbols(editPath, editSrc)

	const maxMsgs = 3
	var msgs []string
	for _, c := range held {
		if len(msgs) >= maxMsgs {
			break
		}
		// Same workspace only: coupling across separate worktrees is meaningless.
		if c.Holder == callsign || c.Workspace != workspace || langOf(c.Path) != editLang {
			continue
		}
		fSrc, err := os.ReadFile(absUnder(c.Path, cwd))
		if err != nil {
			continue
		}
		// The edit uses a symbol the held file defines (this file calls into it).
		if len(editSrc) > 0 {
			for _, s := range definedSymbols(c.Path, fSrc) {
				if referencesSymbol(editSrc, s) {
					msgs = append(msgs, fmt.Sprintf("%s is editing %s, which defines %s that this file uses.", c.Holder, c.Path, s))
					break
				}
			}
		}
		if len(msgs) >= maxMsgs {
			break
		}
		// The held file uses a symbol the edit defines (others depend on this).
		for _, s := range editDefs {
			if referencesSymbol(fSrc, s) {
				msgs = append(msgs, fmt.Sprintf("this file defines %s, which %s is editing in %s.", s, c.Holder, c.Path))
				break
			}
		}
	}
	return msgs
}

// absUnder resolves p against cwd unless it is already absolute, matching how the
// hook keys clearance paths.
func absUnder(p, cwd string) string {
	if filepath.IsAbs(p) || cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}
