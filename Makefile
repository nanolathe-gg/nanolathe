.PHONY: check build test kaiju-content

check:
	@./tools/check

build:
	@go build ./...

test:
	@go test ./...

# Kaiju needs its stock shaders/materials present as a content database.
# See docs/PLAN_00_BOOTSTRAP.md WU-00-1.
kaiju-content:
	@test -d content || cp -R ../kaiju/src/editor/editor_embedded_content/editor_content content
	@echo "content/ ready"
