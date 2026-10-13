# Taproot build targets.

# Build the pure review engine as a WASM module for the browser surfaces
# (PWA, extension) and vendor it alongside the matching wasm_exec.js glue
# from the local Go toolchain. Both are SHA256-pinned like every other
# vendored artifact. The WASM is committed, so it must not stamp the commit
# or dirty state it was built from.
.PHONY: core-wasm
core-wasm:
	mkdir -p vendor/core
	GOOS=js GOARCH=wasm go build -trimpath -buildvcs=false -o vendor/core/taproot-core.wasm ./core/wasmmain
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" vendor/core/wasm_exec.js
	cd vendor/core && sha256sum taproot-core.wasm wasm_exec.js > checksums.txt
	@echo "vendored:" && cat vendor/core/checksums.txt

.PHONY: test
test:
	go test ./...
	node --test shared/test/*.test.mjs

# Dev-only Go↔WASM parity check (requires node; not a runtime dependency).
# It checks the committed WASM, so run make core-wasm first after changing /core.
.PHONY: parity
parity:
	node scripts/parity-check.mjs

.PHONY: cli
cli:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o taproot ./apps/cli/cmd/taproot

# Release artifacts in dist/: the CLI for linux and darwin on amd64 and arm64,
# LICENSE and THIRD_PARTY_NOTICES, the extension zip named after its manifest
# version, and SHA256SUMS.
VERSION ?= $(shell git describe --tags --always --dirty)

.PHONY: dist
dist:
	@s=$$(git status --porcelain) && test -z "$$s" || { echo "make dist: the tree has changes; commit or stash them first" >&2; exit 1; }
	sh scripts/sync-extension-lib.sh --check
	node scripts/parity-check.mjs
	rm -rf dist
	mkdir dist
	for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
		os=$${t%/*} arch=$${t#*/}; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags "-s -w -X main.version=$(VERSION)" \
			-o dist/taproot-$(VERSION)-$$os-$$arch ./apps/cli/cmd/taproot || exit 1; \
	done
	cp LICENSE THIRD_PARTY_NOTICES dist/
	ext=$$(sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' apps/extension/manifest.json) && \
		test -n "$$ext" && \
		(cd apps/extension && git ls-files | zip -q -X -@ ../../dist/taproot-extension-$$ext.zip) && \
		zip -q -X -j dist/taproot-extension-$$ext.zip LICENSE THIRD_PARTY_NOTICES
	cd dist && sha256sum taproot-* > SHA256SUMS
	@cat dist/SHA256SUMS
