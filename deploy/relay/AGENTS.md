# Nanolathe relay deployment snapshot

This repository is a generated deployment snapshot of the Nanolathe engine's
relay. UPSTREAM.json records the source revision and file digests. Runtime
code in cmd/ and internal/ is maintained upstream: make protocol changes there
and export a fresh snapshot with tools/export-relay. Do not develop a separate
relay protocol here. Templates under deploy/relay upstream own this README,
Dockerfile and workflow; the app's settings live in App Platform. The only
import rewrite is the Go module prefix from nanolathe to nanolathe-relay.

Use a worktree for edits. Keep this repository private. Never add retail assets,
credentials, TLS keys, or engine simulation/rendering code. Tests and builds
must use only the standard library. Run go vet ./..., go test -race ./..., and
docker build . before publishing. App Platform runs exactly one instance and
deploys every push to main; redeploys/restarts abort active games. Do not
deploy paid resources without the maintainer's instruction.
