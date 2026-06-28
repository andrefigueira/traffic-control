package main

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/andrefigueira/traffic-control/internal/client"
)

// cmdScope opens the live dashboard the tower serves. It confirms the tower is
// up first, prints the URL, and makes a best-effort attempt to open a browser.
func cmdScope(_ []string) error {
	addr := client.Addr()
	url := "http://" + addr + "/scope"
	c, ctx, cancel := cli()
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		return fmt.Errorf("tower not reachable at %s (start it with `tc serve`): %w", addr, err)
	}
	fmt.Println("the scope:", url)
	openBrowser(url)
	return nil
}

// openBrowser tries to open url in the default browser, silently doing nothing
// if it cannot.
func openBrowser(url string) {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	_ = exec.Command(name, args...).Start()
}
