// Command yousage-relay serves encrypted snapshots and expiring receipt shares.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"yousage/internal/relay"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Getenv, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "yousage-relay:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environment func(string) string, output io.Writer) error {
	server, err := relay.New(relay.Config{
		Token: environment("YOUSAGE_RELAY_TOKEN"), DataDir: environment("YOUSAGE_RELAY_DATA"),
	})
	if err != nil {
		return err
	}
	defer server.Close()
	address := environment("YOUSAGE_RELAY_ADDR")
	if address == "" {
		address = ":8080"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	log.New(output, "", log.LstdFlags).Printf("relay listening on %s", listener.Addr())
	return server.Serve(ctx, listener)
}
