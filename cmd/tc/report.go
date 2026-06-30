package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// cmdReport summarizes the activity log into a performance/usage report: how many
// agents flew, how many clearances were granted, and crucially how many
// collisions were caught. It also folds in the tower's current live stats when
// it is reachable.
func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	path := fs.String("log", eventLogPath(), "activity log to read")
	_ = fs.Parse(args)

	rep, err := buildReport(*path)
	if err != nil {
		return err
	}
	printReport(*path, rep)

	// Live snapshot, if a tower is up.
	c, ctx, cancel := cli()
	defer cancel()
	if h, err := c.Health(ctx); err == nil {
		fmt.Println("\nlive now:")
		fmt.Printf("  flying:     %s\n", numField(h, "sessions"))
		fmt.Printf("  held:       %s clearance(s)\n", numField(h, "clearances"))
		fmt.Printf("  watchers:   %s\n", numField(h, "subscribers"))
		if d := numField(h, "dropped_events"); d != "0" {
			fmt.Printf("  warning:    %s frequency event(s) dropped to slow subscribers\n", d)
		}
		if up := uptime(h); up != "" {
			fmt.Printf("  uptime:     %s\n", up)
		}
	}
	return nil
}

// report is the aggregated view of the activity log.
type report struct {
	events    int
	first     time.Time
	last      time.Time
	byType    map[string]int
	callsigns map[string]bool
}

// buildReport reads the JSONL activity log and aggregates it. A missing log is
// not an error: it just yields an empty report, since the tower may not have run
// yet or logging may be off.
func buildReport(path string) (report, error) {
	rep := report{byType: map[string]int{}, callsigns: map[string]bool{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rep, nil
		}
		return rep, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev protocol.Event
		if json.Unmarshal(line, &ev) != nil {
			continue // skip a torn or partial line rather than failing the report
		}
		rep.events++
		rep.byType[ev.Type]++
		if rep.first.IsZero() || ev.At.Before(rep.first) {
			rep.first = ev.At
		}
		if ev.At.After(rep.last) {
			rep.last = ev.At
		}
		if m, ok := ev.Payload.(map[string]interface{}); ok {
			for _, k := range []string{"callsign", "holder", "requester", "held_by"} {
				if v, ok := m[k].(string); ok && v != "" {
					rep.callsigns[v] = true
				}
			}
		}
	}
	return rep, sc.Err()
}

func printReport(path string, rep report) {
	fmt.Println("Traffic Control activity report")
	fmt.Printf("  log: %s\n", path)
	if rep.events == 0 {
		fmt.Println("  no activity recorded yet (has the tower been running with logging on?)")
		return
	}
	fmt.Printf("  window:   %s  ->  %s  (%s)\n",
		rep.first.Format("2006-01-02 15:04"), rep.last.Format("2006-01-02 15:04"),
		rep.last.Sub(rep.first).Round(time.Second))
	fmt.Printf("  events:   %d\n", rep.events)
	fmt.Printf("  agents seen: %d  %v\n", len(rep.callsigns), sortedKeys(rep.callsigns))
	fmt.Println("  coordination:")
	fmt.Printf("    clearances granted: %d\n", rep.byType[protocol.EventClearanceGranted])
	fmt.Printf("    conflicts caught:   %d   <- collisions blocked or warned\n", rep.byType[protocol.EventConflictAlert])
	fmt.Printf("    advisory overlaps:  %d\n", rep.byType[protocol.EventAdvisoryOverlap])
	fmt.Printf("    handoffs:           %d\n", rep.byType[protocol.EventClearanceHandoff])
	fmt.Printf("    lease expirations:  %d\n", rep.byType[protocol.EventClearanceExpired])
	fmt.Printf("    board posts:        %d\n", rep.byType[protocol.EventBoardPosted])
	fmt.Printf("    joins / leaves:     %d / %d\n", rep.byType[protocol.EventPresenceJoin], rep.byType[protocol.EventPresenceLeave])
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// numField renders a numeric health field as an integer string (health values
// arrive as JSON float64).
func numField(h map[string]interface{}, key string) string {
	if v, ok := h[key].(float64); ok {
		return fmt.Sprintf("%d", int(v))
	}
	return "0"
}

// uptime derives a human duration from the health snapshot's started_at.
func uptime(h map[string]interface{}) string {
	s, ok := h["started_at"].(string)
	if !ok {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return ""
	}
	return time.Since(t).Round(time.Second).String()
}
