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
// Go-only, opt-in (TC_SYMBOLS=1) advisory that closes part of that gap: when the
// file about to be edited and a file another agent holds share an exported Go
// symbol that one defines and the other uses, the agent is told. It is regex
// over source rather than a real parser, so it favours the common cases and
// accepts the odd miss, and it only ever warns, never blocks.

var (
	goFuncDecl = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Z]\w*)\s*\(`)
	goTypeDecl = regexp.MustCompile(`(?m)^type\s+([A-Z]\w*)`)
)

// noiseSymbols are exported names so common (interface methods, ubiquitous
// helpers) that coupling on them is almost always a false positive.
var noiseSymbols = map[string]bool{
	"String": true, "Error": true, "Read": true, "Write": true, "Close": true,
	"Len": true, "Less": true, "Swap": true, "ServeHTTP": true,
	"MarshalJSON": true, "UnmarshalJSON": true, "Bytes": true,
}

// definedGoSymbols extracts exported top-level functions, methods, and types
// from Go source. Only exported (capitalized) names of four or more characters
// that are not in the noise list are returned, since those are the ones another
// file can meaningfully couple to.
func definedGoSymbols(src []byte) []string {
	seen := map[string]bool{}
	var out []string
	collect := func(re *regexp.Regexp) {
		for _, m := range re.FindAllSubmatch(src, -1) {
			s := string(m[1])
			if len(s) < 4 || seen[s] || noiseSymbols[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	collect(goFuncDecl)
	collect(goTypeDecl)
	return out
}

// referencesSymbol reports whether sym appears as a whole word in src, so "Foo"
// matches a call to Foo but not FooBar or BarFoo.
func referencesSymbol(src []byte, sym string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(sym) + `\b`)
	return re.Match(src)
}

// symbolCoupling returns advisory lines describing semantic coupling between the
// file about to be edited and the Go files other agents currently hold. It reads
// from disk, so it is best-effort: a missing file, a non-Go path, or a new file
// with nothing on disk yet simply yields fewer or no lines. Capped so a busy
// tree cannot produce a wall of warnings.
func symbolCoupling(editPath, cwd string, held []protocol.Clearance, callsign string) []string {
	if !strings.HasSuffix(editPath, ".go") {
		return nil
	}
	editSrc, _ := os.ReadFile(absUnder(editPath, cwd)) // empty for a not-yet-created file
	editDefs := definedGoSymbols(editSrc)

	const maxMsgs = 3
	var msgs []string
	for _, c := range held {
		if len(msgs) >= maxMsgs {
			break
		}
		if c.Holder == callsign || !strings.HasSuffix(c.Path, ".go") {
			continue
		}
		fSrc, err := os.ReadFile(absUnder(c.Path, cwd))
		if err != nil {
			continue
		}
		// The edit uses a symbol the held file defines (this file calls into it).
		if len(editSrc) > 0 {
			for _, s := range definedGoSymbols(fSrc) {
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
