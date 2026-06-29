package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"strings"
	"syscall"
	"time"

	"github.com/andrefigueira/traffic-control/internal/client"
)

// parseFlags parses fs while allowing flags and positional arguments to be
// interspersed, which Go's flag package does not do on its own. Without this,
// `tc clearance PATH --mode exclusive` would silently drop --mode. Returns the
// collected positional arguments in order.
func parseFlags(fs *flag.FlagSet, args []string) []string {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return positionals
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positionals
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}

// resolveCallsign picks the identity to act as: an explicit flag wins, then
// TC_CALLSIGN, then a derived user@host so a human at a terminal still shows up
// sensibly on the board.
func resolveCallsign(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := strings.TrimSpace(os.Getenv("TC_CALLSIGN")); v != "" {
		return v
	}
	name := "agent"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, _ := os.Hostname()
	if host != "" {
		return fmt.Sprintf("%s@%s", name, host)
	}
	return name
}

// cli returns a tower client and a short-lived context for one-shot commands.
func cli() (*client.Client, context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	return client.FromEnv(), ctx, cancel
}

// mustCli returns a client for long-running commands like watch.
func mustCli() *client.Client { return client.FromEnv() }

// backgroundCtx is a context cancelled on ctrl-c, for streaming commands.
func backgroundCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// sleepCtx waits for d or until ctx is cancelled, returning false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// splitCSV turns "a,b , c" into ["a","b","c"], dropping blanks.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ago renders a compact relative time like "3m" or "12s".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}
