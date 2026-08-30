.PHONY: check check-all build test test-retail test-desktop test-headless

GO_TEST_PACKAGES := $(shell go list ./... | grep -vE '/(cmd/nanolathe|internal/client)$$')

check:
	@./tools/check

# check-all is the auditable gate: it verifies gofmt and runs build/vet/test
# over the entire module INCLUDING desktop packages (cmd/nanolathe,
# internal/client). Retail-asset-dependent tests remain opt-in via
# $NANOLATHE_TA_ROOT (or $NANOLATHE_RETAIL_ASSETS) and skip with a clear
# message when assets are absent; no retail assets are required.
check-all:
	@echo "==> gofmt"
	@unformatted=$$(gofmt -l . || true); \
	if [ -n "$$unformatted" ]; then echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; fi
	@echo "==> go build"
	@go build ./...
	@echo "==> go vet"
	@go vet ./...
	@echo "==> go test (all packages including desktop, no retail assets required)"
	@go test ./...

build:
	@go build ./...

test:
	@go test $(GO_TEST_PACKAGES)

# Full-install corpus gates are opt-in: they parse all shipped formats,
# compile the complete retail catalog, and load every retail map.
test-retail:
	@go test -tags retail $(GO_TEST_PACKAGES)

# Desktop packages retain Ebitengine window/device dependencies. Keep them
# explicit while the displayless command and runner prove the authoritative
# composition and Step topology independently [I6].
test-desktop:
	@go test ./internal/client ./cmd/nanolathe

test-headless:
	@go test ./internal/headless ./cmd/nanolathe-headless
	@go build -o /tmp/nanolathe-headless ./cmd/nanolathe-headless
