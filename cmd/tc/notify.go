package main

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/andrefigueira/traffic-control/internal/protocol"
)

// Desktop notifications for the events that matter when you are not staring at
// the scope: a hard conflict, or a soft overlap. This is the OS-level signal
// beyond the in-page frequency, fired by `tc watch --notify`. Like openBrowser,
// it is best-effort: if the platform's notifier is missing it simply does
// nothing rather than erroring.

// notifyMessage turns an event into a notification title and body, and reports
// whether the event is one worth surfacing at all. It is pure so it can be
// tested without firing anything.
func notifyMessage(ev protocol.Event) (title, body string, ok bool) {
	m, isMap := ev.Payload.(map[string]interface{})
	if !isMap {
		return "", "", false
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	switch ev.Type {
	case protocol.EventConflictAlert:
		return "Traffic Control: conflict",
			fmt.Sprintf("%s reached for %s, held by %s", str("requester"), str("path"), str("held_by")),
			true
	case protocol.EventAdvisoryOverlap:
		return "Traffic Control: overlap",
			fmt.Sprintf("%s overlaps %s on %s", str("requester"), str("held_by"), str("path")),
			true
	default:
		return "", "", false
	}
}

// notifyCommand returns the OS command that pops a desktop notification, or an
// empty name on platforms without a built-in one (currently Windows). Separated
// from notify so the platform mapping is testable.
func notifyCommand(title, body string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "osascript", []string{"-e", fmt.Sprintf("display notification %q with title %q", body, title)}
	case "linux":
		return "notify-send", []string{title, body}
	default:
		return "", nil
	}
}

// notify fires a desktop notification, best-effort.
func notify(title, body string) {
	name, args := notifyCommand(title, body)
	if name == "" {
		return
	}
	_ = exec.Command(name, args...).Start()
}

// maybeNotify fires a notification for an event if it is one worth surfacing.
func maybeNotify(ev protocol.Event) {
	if title, body, ok := notifyMessage(ev); ok {
		notify(title, body)
	}
}
