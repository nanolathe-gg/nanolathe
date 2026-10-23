// Command nanolathe-relay-check verifies hosted admission without game assets.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

const defaultAddress = "wss://relay.nanolathe.gg/relay"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("nanolathe-relay-check", flag.ContinueOnError)
	flags.SetOutput(out)
	address := flags.String("address", defaultAddress, "hosted relay wss:// URL or TLS host:port")
	insecure := flags.Bool("insecure-loopback", false, "plaintext on numeric loopback for local tests only")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("nanolathe: relay compatibility check failed: logical path arguments, providers searched [command line], expected flags only")
	}
	if err := checkRelay(ctx, *address, relay.HostedDialOptions{InsecureLoopback: *insecure}, out, time.Second); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Relay compatible: command schema %d at %s (create, describe, join; both seats present)\n", netproto.CommandSchemaVersion, *address)
	return err
}

// Exercise the production lobby path with the build's own command schema
// (DESIGN_MULTIPLAYER §8.2, §12.2, §16.6.1). The configuration is opaque to
// the relay; neither client readies or starts a battle.
func checkRelay(ctx context.Context, address string, options relay.HostedDialOptions, out io.Writer, retryDelay time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	const attempts = 3
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return checkError(address, "deadline", err)
		}
		err := checkRelayAttempt(ctx, address, options)
		if err == nil {
			return nil
		}
		var handshake *relay.WebSocketHandshakeError
		if attempt == attempts || !errors.As(err, &handshake) {
			return err
		}
		// Retry only explicit gateway/service failures. Invalid upgrades, TLS
		// failures and hosted refusals still block release (DESIGN_MULTIPLAYER §16.5.6).
		switch handshake.StatusCode {
		case 502, 503, 504: // Bad Gateway, Service Unavailable, Gateway Timeout.
		default:
			return err
		}
		delay := time.Duration(attempt) * retryDelay
		if _, writeErr := fmt.Fprintf(out, "Relay check attempt %d/%d failed at %s: %v; retrying in %s\n", attempt, attempts, time.Now().UTC().Format(time.RFC3339), err, delay); writeErr != nil {
			return writeErr
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w: %w", err, ctx.Err())
		case <-timer.C:
		}
	}
}

// Each failed attempt closes its own clients before another creates a room.
func checkRelayAttempt(ctx context.Context, address string, options relay.HostedDialOptions) error {
	config := []byte("nanolathe relay compatibility check")
	hello := relay.LocalHello{Identity: netproto.Identity{
		Protocol: netproto.CommandSchemaVersion,
		Rules:    netproto.RuleIdentity{Name: "relay-check"},
	}}
	host, err := relay.OpenHostedLobby(ctx, address, "", hello, config, 2, options)
	if err != nil {
		return checkError(address, "create", err)
	}
	defer host.Close()
	description, err := relay.DescribeHostedRoom(ctx, address, host.Code(), options)
	if err != nil {
		return checkError(address, "describe", err)
	}
	if description.Size != 2 || !bytes.Equal(description.Config, config) {
		return checkError(address, "describe", errors.New("room size or configuration changed"))
	}
	joiner, err := relay.OpenHostedLobby(ctx, address, host.Code(), hello, nil, 0, options)
	if err != nil {
		return checkError(address, "join", err)
	}
	defer joiner.Close()
	if host.Seat() != 0 || joiner.Seat() != 1 {
		return checkError(address, "join", errors.New("clients were not assigned seats 0 and 1"))
	}
	for i, lobby := range []*relay.HostedLobby{host, joiner} {
		if err := awaitBothSeats(ctx, lobby); err != nil {
			return checkError(address, fmt.Sprintf("seat %d lobby state", i), err)
		}
	}
	return nil
}

func awaitBothSeats(ctx context.Context, lobby *relay.HostedLobby) error {
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := lobby.State()
		if err != nil {
			return err
		}
		if state.Started || state.Mismatch || (state.Size != 0 && state.Size != 2) {
			return errors.New("unexpected lobby state before readiness")
		}
		if state.Size == 2 && state.Seats[0].Present && state.Seats[1].Present {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		}
	}
}

func checkError(address, stage string, err error) error {
	return fmt.Errorf("nanolathe: relay compatibility check failed: logical path %s, providers searched [%s], expected a two-seat lobby using command schema %d: %w", stage, address, netproto.CommandSchemaVersion, err)
}
