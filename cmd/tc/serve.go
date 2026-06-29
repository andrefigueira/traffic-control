package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/andrefigueira/traffic-control/internal/api"
	"github.com/andrefigueira/traffic-control/internal/client"
	"github.com/andrefigueira/traffic-control/internal/tower"
)

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", client.Addr(), "address to listen on (host:port)")
	_ = fs.Parse(args)

	// Bind before any side effects. If the port is taken, a tower is already
	// running, so we fail cleanly without touching the pidfile.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("could not bind %s; a tower may already be running there (try `tc status`): %w", *addr, err)
	}

	tw := tower.New()
	srv := api.New(tw)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Claim the pidfile only after the bind succeeds, so a tower that lost the
	// race for the port cannot overwrite the live tower's pidfile.
	if err := writePidFile(); err == nil {
		defer removePidFile()
	}

	fmt.Fprintf(os.Stderr, "tower up on %s  (the scope: http://%s/)\n", *addr, *addr)
	return srv.ServeListener(ctx, ln)
}
