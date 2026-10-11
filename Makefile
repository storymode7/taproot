# Astra build targets.

# Build the pure review engine as a WASM module for the browser surfaces
# (PWA, extension) and vendor it alongside the matching wasm_exec.js glue
# from the local Go toolchain. Both are SHA256-pinned like every other
# vendored artifact.
.PHONY: core-wasm
core-wasm:
	mkdir -p vendor/core
	GOOS=js GOARCH=wasm go build -trimpath -o vendor/core/astra-core.wasm ./core/wasmmain
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" vendor/core/wasm_exec.js
	cd vendor/core && sha256sum astra-core.wasm wasm_exec.js > checksums.txt
	@echo "vendored:" && cat vendor/core/checksums.txt

.PHONY: test
test:
	go test ./...

# Dev-only Go↔WASM parity check (requires node; not a runtime dependency).
.PHONY: parity
parity: core-wasm
	node scripts/parity-check.mjs

.PHONY: cli
cli:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o astra ./apps/cli/cmd/astra
