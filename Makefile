.PHONY: check build test test-retail test-desktop kaiju-content

GO_TEST_PACKAGES := $(shell go list ./... | grep -vE '/(cmd/nanolathe|internal/client)$$')

check:
	@./tools/check

build:
	@go build ./...

test:
	@go test $(GO_TEST_PACKAGES)

# Full-install corpus gates are opt-in: they parse all shipped formats,
# compile the complete retail catalog, and load every retail map.
test-retail:
	@go test -tags retail $(GO_TEST_PACKAGES)

# Ebiten initializes GLFW/AppKit during package initialization. Keep the
# desktop-only packages explicit so the default loop stays usable headlessly.
test-desktop:
	@go test ./internal/client ./cmd/nanolathe

# Kaiju needs its stock shaders/materials present as a content database.
# See docs/PLAN_00_BOOTSTRAP.md WU-00-1.
kaiju-content:
	@test -d content || cp -R ../kaiju/src/editor/editor_embedded_content/editor_content content
	@echo "content/ ready"
