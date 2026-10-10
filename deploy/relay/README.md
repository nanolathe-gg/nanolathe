# Nanolathe relay

Small, standalone relay for Nanolathe's online play: lobbies with room codes,
then skirmish for 2-10 players and Survival for 2-3. The clients simulate the
battle; this service forwards commands and tick grants. No game assets, GPU,
database, or game engine are needed on the server.

This repository is private. Its deployed endpoint is reachable publicly; room
codes are invitations, not user accounts. There is no reconnect, spectating or
distributed room directory.

## Deploy on DigitalOcean App Platform

The live service is `https://relay.nanolathe.gg` (App Platform app
`nanolathe-relay`, region `sfo`). Its settings live in App Platform; this
repository holds no app spec. `doctl apps spec get APP-ID` shows them.

- **Pushing to `main` deploys.** Autodeploy is on, so every push to this
  repository's `main` branch builds and deploys it. Confirm afterwards that
  `curl https://relay.nanolathe.gg/healthz` returns `ok`.
- **One always-running instance** of 1 shared vCPU / 512 MiB
  (`apps-s-1vcpu-0.5gb`). The app sets no environment, so the image's defaults
  apply: `GOMEMLIMIT=384MiB` and `GOMAXPROCS=1`. This is not a measured
  capacity claim. Check the displayed bill before changing the size:
  [current pricing](https://docs.digitalocean.com/products/app-platform/details/pricing/).
- **The app configures no HTTP health check.** The image also answers `/healthz` on
  port 8081, outside the relay's connection limit; CI checks it, and a
  platform health check could use it. Port 8080 answers `/healthz` for people
  checking the public URL.
- The custom domain needs both its DNS CNAME and the domain entry on the app
  before App Platform issues its certificate. No certificates or secrets
  belong in this repository.

**Keep instance count at 1.** Rooms live in this process's memory; a second
instance could receive a join for a room on the first. A restart or deployment
ends active matches, so push between play sessions. There is no persistent
disk.

## Connect the local game

Every player needs the same retail content and mod; the lobby's rehearsal
checks that the clients compute the same battle before Start. Players normally
use the main menu's MULTI screen; from the command line, create a room:

```sh
./nanolathe --root ~/TotalAnnihilation --mod none --map 'ashap plateau' --fullscreen=false --relay-address wss://relay.nanolathe.gg/relay
```

This creates the room and prints its six-character code in the terminal and game
message ring. Codes use only `CFHJKMNPRTVWX23456789`. Start the second client
with that room code:

```sh
./nanolathe --root ~/TotalAnnihilation --mod none --map 'ashap plateau' --fullscreen=false --relay-address wss://relay.nanolathe.gg/relay --relay-room K7MP2X
```

Both clients may run on one Mac: their connections still travel through the real
cloud relay. The game starts when both are ready. No `--relay-insecure-loopback`
or custom CA is needed with the managed HTTPS domain. A client that hears
nothing from the relay for 25 seconds during a match stops with a message.

## Local container check

```sh
docker build --platform linux/amd64 -t nanolathe-relay .
docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges -p 127.0.0.1:8080:8080 -p 127.0.0.1:8081:8081 nanolathe-relay
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8081/healthz
```

For this local test only, the game uses `--relay-address ws://127.0.0.1:8080/relay
--relay-insecure-loopback`. Production uses `wss://`. The Docker command's
`--behind-tls-proxy` explicitly permits HTTP inside the platform; do not expose
that listener directly to the public Internet without an HTTPS proxy.

The final image is a static non-root Go executable on scratch. Its default
command serves 128 rooms and 512 connections with `GOMEMLIMIT=384MiB` for the
512 MiB instance. Each room's bytes are bounded (DESIGN_MULTIPLAYER §16.5.1),
so a misbehaving client fails only its own room. Raise `--max-rooms` only with
more memory. Capacity/load tests are separate from the functional tests here.

## Updating the snapshot

UPSTREAM.json records the engine source revision, original and exported hashes.
Export a new **empty** staging directory from a reviewed engine commit:

```sh
# In the Nanolathe worktree:
tools/export-relay /path/to/empty/staging --revision COMMIT
```

Review the exported files before updating this private repository. The exporter
copies only the relay, wire helpers, command and deployment templates; rewrites
the Go module prefix; and introduces no dependencies. Keep protocol development
upstream to avoid drift. CI runs tests, race detection, and container health.

References: [DigitalOcean WebSocket sample](https://github.com/digitalocean/sample-websocket),
[App spec](https://docs.digitalocean.com/products/app-platform/reference/app-spec/),
[platform limits](https://docs.digitalocean.com/products/app-platform/details/limits/).
