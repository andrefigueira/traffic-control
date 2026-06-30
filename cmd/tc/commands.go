package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

func cmdStatus(args []string) error {
	c, ctx, cancel := cli()
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return err
	}
	sessions, err := c.WhosFlying(ctx)
	if err != nil {
		return err
	}
	clrs, err := c.Clearances(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("tower is up. %d flying, %d clearances held.\n", len(sessions), len(clrs))
	for _, s := range sessions {
		fmt.Printf("  %-28s seen %s ago\n", s.Callsign, ago(s.LastSeen))
	}
	// A climbing dropped-events count means a watcher or the scope is not
	// keeping up and may be missing conflict alerts. Surface it loudly.
	if h, err := c.Health(ctx); err == nil {
		if d, ok := h["dropped_events"].(float64); ok && d > 0 {
			fmt.Printf("  warning: %d frequency event(s) dropped to slow subscribers\n", int(d))
		}
	}
	return nil
}

// cmdBoardPost backs both `flightplan` and `done`.
func cmdBoardPost(args []string, kind string) error {
	fs := flag.NewFlagSet(kind, flag.ExitOnError)
	callsign := fs.String("callsign", "", "your identity on the board")
	paths := fs.String("paths", "", "comma-separated paths this is about")
	pos := parseFlags(fs, args)

	msg := strings.TrimSpace(strings.Join(pos, " "))
	if msg == "" {
		return fmt.Errorf("a message is required: tc %s \"what you are doing\"", kind)
	}
	cwd, ws := cwdWorkspace()
	planPaths := splitCSV(*paths)
	for i, p := range planPaths {
		planPaths[i] = keyPath(p, cwd, ws)
	}
	c, ctx, cancel := cli()
	defer cancel()
	e, err := c.PostBoard(ctx, resolveCallsign(*callsign), ws, kind, msg, planPaths)
	if err != nil {
		return err
	}
	fmt.Printf("posted to the board: [%s] %s\n", e.Kind, e.Message)
	return nil
}

func cmdClearance(args []string) error {
	fs := flag.NewFlagSet("clearance", flag.ExitOnError)
	callsign := fs.String("callsign", "", "your identity")
	mode := fs.String("mode", protocol.ModeAdvisory, "advisory|exclusive")
	note := fs.String("note", "", "optional note shown to others")
	ttl := fs.Int("ttl", 0, "lease length in seconds (0 = tower default)")
	pos := parseFlags(fs, args)

	if len(pos) < 1 {
		return fmt.Errorf("a path is required: tc clearance internal/api/server.go")
	}
	cwd, ws := cwdWorkspace()
	path := keyPath(pos[0], cwd, ws)
	c, ctx, cancel := cli()
	defer cancel()
	res, err := c.RequestClearance(ctx, resolveCallsign(*callsign), ws, path, *mode, *note, *ttl)
	if err != nil {
		return err
	}
	if !res.Granted {
		return fmt.Errorf("DENIED: %s", res.Message)
	}
	fmt.Printf("CLEARED: %s\n", res.Message)
	return nil
}

func cmdHandoff(args []string) error {
	fs := flag.NewFlagSet("handoff", flag.ExitOnError)
	callsign := fs.String("callsign", "", "your identity")
	pos := parseFlags(fs, args)

	path := ""
	if len(pos) > 0 {
		path = pos[0]
	}
	c, ctx, cancel := cli()
	defer cancel()
	n, err := c.Handoff(ctx, resolveCallsign(*callsign), path)
	if err != nil {
		return err
	}
	fmt.Printf("handed off %d clearance(s)\n", n)
	return nil
}

func cmdCheck(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("a path is required: tc check internal/api/server.go")
	}
	cwd, ws := cwdWorkspace()
	c, ctx, cancel := cli()
	defer cancel()
	res, err := c.Check(ctx, ws, keyPath(args[0], cwd, ws))
	if err != nil {
		return err
	}
	if !res.Held {
		fmt.Printf("%s is clear\n", args[0])
		return nil
	}
	fmt.Printf("%s is held by %s (%s)\n", args[0], res.Clearance.Holder, res.Clearance.Mode)
	return nil
}

func cmdWhosFlying(args []string) error {
	c, ctx, cancel := cli()
	defer cancel()
	sessions, err := c.WhosFlying(ctx)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("nobody is flying right now")
		return nil
	}
	for _, s := range sessions {
		proj := s.Project
		if proj == "" {
			proj = "-"
		}
		fmt.Printf("%-28s %-20s seen %s ago\n", s.Callsign, proj, ago(s.LastSeen))
	}
	return nil
}

func cmdBoard(args []string) error {
	fs := flag.NewFlagSet("board", flag.ExitOnError)
	limit := fs.Int("limit", 20, "how many entries to show")
	_ = fs.Parse(args)

	c, ctx, cancel := cli()
	defer cancel()
	entries, err := c.ReadBoard(ctx, *limit)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("the board is empty")
		return nil
	}
	for _, e := range entries {
		paths := ""
		if len(e.Paths) > 0 {
			paths = "  (" + strings.Join(e.Paths, ", ") + ")"
		}
		fmt.Printf("%s  %-22s [%s] %s%s\n", e.PostedAt.Format("15:04:05"), e.Callsign, e.Kind, e.Message, paths)
	}
	return nil
}

func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	doNotify := fs.Bool("notify", false, "pop a desktop notification on conflict and overlap events")
	_ = fs.Parse(args)

	c := mustCli()
	ctx, cancel := backgroundCtx()
	defer cancel()
	fmt.Println("tuned to the frequency. ctrl-c to leave.")

	// Reconnect with backoff if the stream drops or the tower restarts, so a
	// long-lived watcher survives a tower bounce the way the browser scope does,
	// rather than exiting silently the first time the connection blips. Always
	// sleep before reconnecting, so a tower that accepts the connection then
	// instantly drops the stream cannot spin this loop hot; reset the backoff
	// only after a healthy stream that actually delivered an event.
	const minBackoff = 500 * time.Millisecond
	const maxBackoff = 10 * time.Second
	backoff := minBackoff
	for ctx.Err() == nil {
		events, err := c.Events(ctx)
		gotEvent := false
		if err == nil {
			for ev := range events {
				gotEvent = true
				fmt.Printf("%s  %-22s %v\n", ev.At.Format("15:04:05"), ev.Type, summarize(ev.Payload))
				if *doNotify {
					maybeNotify(ev)
				}
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if gotEvent {
			backoff = minBackoff // the stream was healthy; reset
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "frequency unreachable, retrying in %s...\n", backoff)
		} else {
			fmt.Fprintf(os.Stderr, "frequency dropped, reconnecting in %s...\n", backoff)
		}
		if !sleepCtx(ctx, backoff) {
			return nil
		}
		if !gotEvent {
			backoff = min(backoff*2, maxBackoff) // still flapping; grow the wait
		}
	}
	return nil
}

// summarize renders an event payload compactly for the watch stream.
func summarize(payload interface{}) string {
	m, ok := payload.(map[string]interface{})
	if !ok {
		return ""
	}
	// requester/held_by cover conflict.alert and clearance.advisory payloads,
	// which carry no callsign/holder key, so the line names an agent rather than
	// falling through to the path.
	for _, k := range []string{"callsign", "holder", "requester", "held_by", "path", "message"} {
		if v, ok := m[k]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}
