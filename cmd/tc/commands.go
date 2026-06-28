package main

import (
	"flag"
	"fmt"
	"strings"

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
	c, ctx, cancel := cli()
	defer cancel()
	e, err := c.PostBoard(ctx, resolveCallsign(*callsign), kind, msg, splitCSV(*paths))
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
	path := pos[0]
	c, ctx, cancel := cli()
	defer cancel()
	res, err := c.RequestClearance(ctx, resolveCallsign(*callsign), path, *mode, *note, *ttl)
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
	c, ctx, cancel := cli()
	defer cancel()
	res, err := c.Check(ctx, args[0])
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
	c := mustCli()
	ctx, cancel := backgroundCtx()
	defer cancel()
	events, err := c.Events(ctx)
	if err != nil {
		return err
	}
	fmt.Println("tuned to the frequency. ctrl-c to leave.")
	for ev := range events {
		fmt.Printf("%s  %-22s %v\n", ev.At.Format("15:04:05"), ev.Type, summarize(ev.Payload))
	}
	return nil
}

// summarize renders an event payload compactly for the watch stream.
func summarize(payload interface{}) string {
	m, ok := payload.(map[string]interface{})
	if !ok {
		return ""
	}
	for _, k := range []string{"callsign", "holder", "path", "message"} {
		if v, ok := m[k]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}
