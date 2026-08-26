#!/bin/sh
# baseline.sh — capture the overnight baseline manifest outside the repo tree.
# See work unit ON-00: commit, toolchain, env, module graph, retail present,
# and the full go build/vet/test including desktop packages.
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

# --- collect inputs ---
COMMIT="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
SHORT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
GO_VERSION="$(go version 2>&1 || true)"
GOOS="$(go env GOOS 2>/dev/null || echo unknown)"
GOARCH="$(go env GOARCH 2>/dev/null || echo unknown)"
GOVERSION_ENV="$(go env GOVERSION 2>/dev/null || echo unknown)"
GOROOT="$(go env GOROOT 2>/dev/null || echo unknown)"
GOPATH="$(go env GOPATH 2>/dev/null || echo unknown)"
GOMOD="$(go env GOMOD 2>/dev/null || echo unknown)"
GO_ENV_JSON="$(go env -json 2>/dev/null | tr -d '\n' | head -c 8000 || echo "{}")"
MOD_GRAPH_HEAD="$(go mod graph 2>&1 | head -n 20 || true)"

# retail-assets-present: spec says checks ~/TotalAnnihilation exists
RETAIL_DIR="$HOME/TotalAnnihilation"
RETAIL_PRESENT="false"
if [ -d "$RETAIL_DIR" ]; then
	if ls "$RETAIL_DIR"/*.hpi >/dev/null 2>&1; then
		RETAIL_PRESENT="true"
	else
		# directory exists but no HPI; still count as present per spec's "exists" check
		RETAIL_PRESENT="true"
	fi
fi

# allow NANOLATHE_RETAIL_ASSETS override for manifest clarity (does not affect present boolean per spec)
if [ -n "${NANOLATHE_RETAIL_ASSETS:-}" ] && [ -d "${NANOLATHE_RETAIL_ASSETS}" ]; then
	RETAIL_DIR="$NANOLATHE_RETAIL_ASSETS"
fi
if [ -n "${NANOLATHE_TA_ROOT:-}" ] && [ -d "${NANOLATHE_TA_ROOT}" ]; then
	RETAIL_DIR="$NANOLATHE_TA_ROOT"
fi

TIMESTAMP="$(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date)"

# --- run checks (capture, never abort early) ---
set +e

GOFMT_OUT="$(gofmt -l . 2>&1 || true)"
if [ -n "$GOFMT_OUT" ]; then
	GOFMT_OK="false"
	GOFMT_EXIT=1
else
	GOFMT_OK="true"
	GOFMT_EXIT=0
fi

BUILD_OUT="$(go build ./... 2>&1)"
BUILD_EXIT=$?
if [ $BUILD_EXIT -eq 0 ]; then BUILD_OK="true"; else BUILD_OK="false"; fi

VET_OUT="$(go vet ./... 2>&1)"
VET_EXIT=$?
if [ $VET_EXIT -eq 0 ]; then VET_OK="true"; else VET_OK="false"; fi

TEST_OUT="$(go test ./... 2>&1)"
TEST_EXIT=$?
if [ $TEST_EXIT -eq 0 ]; then TEST_OK="true"; else TEST_OK="false"; fi

# also capture go test verbose list for failing packages (for manifest)
FAIL_PKGS="$(printf "%s" "$TEST_OUT" | grep -E '^--- FAIL|^FAIL' || true)"

set -e

# --- manifest destination (outside repo tree) ---
MANIFEST_DIR="${OVERNIGHT_MANIFEST_DIR:-/tmp}"
mkdir -p "$MANIFEST_DIR"
MANIFEST_FILE="$MANIFEST_DIR/nanolathe-baseline-$SHORT-$(date +%Y%m%d-%H%M%S 2>/dev/null || echo ts).json"

# JSON escape helper via python if available, else minimal shell escaping
json_escape() {
	# escape backslash, quote, newline, tab for JSON string value
	printf "%s" "$1" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read())[1:-1])' 2>/dev/null || \
		printf "%s" "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e ':a;N;$!ba;s/\n/\\n/g' -e 's/\t/\\t/g'
}

GOFMT_ESC="$(json_escape "$GOFMT_OUT")"
BUILD_ESC="$(json_escape "$BUILD_OUT")"
VET_ESC="$(json_escape "$VET_OUT")"
TEST_ESC="$(json_escape "$TEST_OUT")"
FAIL_ESC="$(json_escape "$FAIL_PKGS")"
GO_VER_ESC="$(json_escape "$GO_VERSION")"
MOD_GRAPH_ESC="$(json_escape "$MOD_GRAPH_HEAD")"
GO_ENV_ESC="$(json_escape "$GO_ENV_JSON")"

cat > "$MANIFEST_FILE" <<JSON
{
  "commit": "$COMMIT",
  "commit_short": "$SHORT",
  "timestamp": "$TIMESTAMP",
  "go_version": "$GO_VER_ESC",
  "goos": "$GOOS",
  "goarch": "$GOARCH",
  "goversion_env": "$GOVERSION_ENV",
  "goroot": "$GOROOT",
  "gopath": "$GOPATH",
  "gomod": "$GOMOD",
  "go_env_json": "$GO_ENV_ESC",
  "module_graph_head": "$MOD_GRAPH_ESC",
  "retail_assets_present": $RETAIL_PRESENT,
  "retail_check_path": "$RETAIL_DIR",
  "checks": {
    "gofmt": {"ok": $GOFMT_OK, "exit": $GOFMT_EXIT, "output": "$GOFMT_ESC"},
    "build": {"ok": $BUILD_OK, "exit": $BUILD_EXIT, "output": "$BUILD_ESC"},
    "vet": {"ok": $VET_OK, "exit": $VET_EXIT, "output": "$VET_ESC"},
    "test": {"ok": $TEST_OK, "exit": $TEST_EXIT, "output": "$TEST_ESC", "failing_packages": "$FAIL_ESC"}
  }
}
JSON

echo "baseline manifest: $MANIFEST_FILE"
echo "commit $SHORT go $GO_VERSION $GOOS/$GOARCH retail_present=$RETAIL_PRESENT"
echo "gofmt ok=$GOFMT_OK build ok=$BUILD_OK vet ok=$VET_OK test ok=$TEST_OK"
if [ "$GOFMT_OK" != "true" ] || [ "$BUILD_OK" != "true" ] || [ "$VET_OK" != "true" ] || [ "$TEST_OK" != "true" ]; then
	echo "baseline: one or more checks failed (see manifest)"
fi
# exit with test/build/vet/gofmt combined status for CI usage, but do not hide manifest
if [ "$GOFMT_OK" != "true" ]; then exit 1; fi
if [ "$BUILD_OK" != "true" ]; then exit 1; fi
if [ "$VET_OK" != "true" ]; then exit 1; fi
if [ "$TEST_OK" != "true" ]; then exit 1; fi
exit 0
