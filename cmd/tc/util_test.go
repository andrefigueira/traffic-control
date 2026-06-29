package main

import (
	"context"
	"flag"
	"strings"
	"testing"
	"time"
)

func TestParseFlagsInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	mode := fs.String("mode", "", "")
	// Positionals and flags interleaved: the whole point of parseFlags.
	pos := parseFlags(fs, []string{"first", "--mode", "exclusive", "second"})
	if *mode != "exclusive" {
		t.Fatalf("mode = %q, want exclusive", *mode)
	}
	if len(pos) != 2 || pos[0] != "first" || pos[1] != "second" {
		t.Fatalf("positionals = %v", pos)
	}
}

func TestParseFlagsFlagFirst(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	note := fs.String("note", "", "")
	pos := parseFlags(fs, []string{"--note", "hello", "thepath"})
	if *note != "hello" {
		t.Fatalf("note = %q", *note)
	}
	if len(pos) != 1 || pos[0] != "thepath" {
		t.Fatalf("positionals = %v", pos)
	}
}

func TestParseFlagsNoPositionals(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	on := fs.Bool("on", false, "")
	pos := parseFlags(fs, []string{"--on"})
	if !*on {
		t.Fatal("flag not parsed")
	}
	if len(pos) != 0 {
		t.Fatalf("expected no positionals, got %v", pos)
	}
}

func TestParseFlagsStopsOnParseError(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{}) // swallow the usage message
	fs.String("known", "", "")
	// An unknown flag makes Parse fail; whatever positionals were gathered before
	// the failure are returned rather than panicking.
	pos := parseFlags(fs, []string{"good", "--bogus"})
	if len(pos) != 1 || pos[0] != "good" {
		t.Fatalf("positionals = %v", pos)
	}
}

func TestResolveCallsign(t *testing.T) {
	t.Run("explicit flag wins over everything", func(t *testing.T) {
		t.Setenv("TC_CALLSIGN", "fromenv")
		if got := resolveCallsign("fromflag"); got != "fromflag" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("env used when no flag", func(t *testing.T) {
		t.Setenv("TC_CALLSIGN", "  fromenv  ")
		if got := resolveCallsign(""); got != "fromenv" {
			t.Fatalf("got %q, want trimmed env", got)
		}
	})
	t.Run("derives a non-empty identity as last resort", func(t *testing.T) {
		t.Setenv("TC_CALLSIGN", "")
		got := resolveCallsign("")
		if got == "" {
			t.Fatal("derived callsign must not be empty")
		}
		if got == "fromflag" || got == "fromenv" {
			t.Fatalf("unexpected derived value %q", got)
		}
	})
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b , c", []string{"a", "b", "c"}},
		{" , a, ,b, ", []string{"a", "b"}},
		{"", nil},
		{"   ", nil},
		{"solo", []string{"solo"}},
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitCSV(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitCSV(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestAgo(t *testing.T) {
	if got := ago(time.Now()); !strings.HasSuffix(got, "s") {
		t.Fatalf("just-now should render in seconds, got %q", got)
	}
	if got := ago(time.Now().Add(-90 * time.Second)); got != "1m" {
		t.Fatalf("90s ago = %q, want 1m", got)
	}
	if got := ago(time.Now().Add(-150 * time.Minute)); got != "2h" {
		t.Fatalf("150m ago = %q, want 2h", got)
	}
}

func TestSleepCtx(t *testing.T) {
	t.Run("returns true when the timer fires", func(t *testing.T) {
		if !sleepCtx(context.Background(), 2*time.Millisecond) {
			t.Fatal("expected true after the duration elapses")
		}
	})
	t.Run("returns false when the context is already done", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if sleepCtx(ctx, time.Hour) {
			t.Fatal("expected false on a cancelled context")
		}
	})
}
