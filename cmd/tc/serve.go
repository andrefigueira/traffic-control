package main

import (
	"context"
	"flag"
	"fmt"
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

	tw := tower.New()
	srv := api.New(tw)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "tower up on %s  (the scope: http://%s/events)\n", *addr, *addr)
	return srv.Serve(ctx, *addr)
}
