#!/bin/sh
# Sync the shared browser modules and the core WASM into each surface.
#
# /shared/lib is the source of truth; extensions cannot reference files
# outside their package and the PWA must be self-contained, so both get a
# checked-in copy. Run after editing /shared/lib or rebuilding the core
# (make core-wasm). Pass --check to verify the copies are in sync (CI).
set -eu

cd "$(dirname "$0")/.."

DESTS="apps/pwa/lib/shared apps/extension/lib/shared"

if [ "${1:-}" = "--check" ]; then
    fail=0
    for d in $DESTS; do
        if ! diff -rq shared/lib "$d" >/dev/null 2>&1; then
            echo "OUT OF SYNC: $d (run scripts/sync-extension-lib.sh)" >&2
            fail=1
        fi
    done
    if ! diff -q vendor/core/astra-core.wasm apps/extension/vendor/core/astra-core.wasm >/dev/null 2>&1 \
       || ! diff -q vendor/core/wasm_exec.js apps/extension/vendor/core/wasm_exec.js >/dev/null 2>&1; then
        echo "OUT OF SYNC: apps/extension/vendor/core (run scripts/sync-extension-lib.sh)" >&2
        fail=1
    fi
    exit $fail
fi

for d in $DESTS; do
    rm -rf "$d"
    mkdir -p "$d"
    cp -r shared/lib/. "$d"/
    echo "synced $d"
done

# The extension package needs its own copy of the core WASM.
mkdir -p apps/extension/vendor/core
cp vendor/core/astra-core.wasm vendor/core/wasm_exec.js vendor/core/checksums.txt apps/extension/vendor/core/
echo "synced apps/extension/vendor/core"
