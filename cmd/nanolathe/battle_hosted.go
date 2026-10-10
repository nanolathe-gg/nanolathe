package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

func hostedDialOptions(o Options) (relay.HostedDialOptions, error) {
	options := relay.HostedDialOptions{InsecureLoopback: o.RelayInsecureLoopback}
	if o.RelayCA == "" {
		return options, nil
	}
	pem, err := os.ReadFile(o.RelayCA)
	if err != nil {
		return options, fmt.Errorf("%w: %v", localMultiplayerError(o.RelayCA, "a readable PEM trust certificate"), err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return options, localMultiplayerError(o.RelayCA, "a valid PEM trust certificate")
	}
	options.TLSConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}
	return options, nil
}

func (b *battleSession) startHostedMultiplayer(o Options, identity netproto.Identity) error {
	options, err := hostedDialOptions(o)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := relay.DialHosted
	if strings.Contains(o.RelayAddress, "://") {
		dial = relay.DialHostedWebSocket
	}
	// Accept a code typed in any case, with spaces or dashes, as the online
	// screen does; the relay refuses an invalid one by name.
	requested := strings.ToUpper(strings.TrimSpace(o.RelayRoom))
	if code, ok := relay.NormalizeRoomCode(o.RelayRoom); ok {
		requested = code
	}
	connection, room, err := dial(ctx, o.RelayAddress, requested, relay.LocalHello{
		Seat: b.sess.LocalOwner, Identity: identity, InitialChecksum: b.sess.UnitStateChecksum(),
	}, options)
	if err != nil {
		return err
	}
	// This seat records the relay stream it plays (replay_record.go).
	client := b.recordHostedReplay(o, identity, connection)
	stats := newOnlineNetStats(client, b.sess.LocalOwner, 2)
	driver, err := lockstep.NewPacedDriver(b.sess, stats)
	if err != nil {
		_ = client.Close()
		return err
	}
	b.multiplayer = &battleMultiplayer{driver: driver, completed: driver.Completed, net: stats}
	b.onlineNotice(fmt.Sprintf("Hosted room %s at %s: seat %d, waiting for both clients", room, o.RelayAddress, b.sess.LocalOwner+1))
	return nil
}

func validateHostedAddress(o Options) error {
	address := o.RelayAddress
	if strings.Contains(address, "://") {
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "ws" && u.Scheme != "wss" || u.Hostname() == "" || u.User != nil || u.Path != "/relay" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(address, "#") {
			return localMultiplayerError("relay URL", "wss://host/relay without credentials, query or fragment")
		}
		port := u.Port()
		if port == "" {
			if strings.HasSuffix(u.Host, ":") {
				return localMultiplayerError("relay URL", "a valid port")
			}
			port = "443"
			if u.Scheme == "ws" {
				port = "80"
			}
		}
		address = net.JoinHostPort(u.Hostname(), port)
		if u.Scheme == "ws" && !o.RelayInsecureLoopback || u.Scheme == "wss" && o.RelayInsecureLoopback {
			return localMultiplayerError("WebSocket transport", "wss://, or ws:// with explicit loopback test mode")
		}
	}
	host, port, err := net.SplitHostPort(address)
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || portErr != nil || n < 1 || n > 65535 {
		return localMultiplayerError("relay address", "host:port or wss://host/relay with a valid port")
	}
	if o.RelayInsecureLoopback {
		a, err := netip.ParseAddrPort(address)
		if err != nil || !a.Addr().IsLoopback() || a.Addr().Zone() != "" || o.RelayCA != "" {
			return localMultiplayerError("plaintext relay", "a numeric loopback address without a TLS certificate option")
		}
	}
	return nil
}
