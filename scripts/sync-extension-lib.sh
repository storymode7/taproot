#!/bin/sh
# Sync the shared browser modules and the core WASM into each surface.
#
# /shared/lib and vendor/core are the source of truth. The extension is
# packaged on its own and the PWA can be published from apps/pwa/ alone,
# so each gets a checked-in copy of both. The PWA also gets LICENSE and
# THIRD_PARTY_NOTICES, so that folder alone carries the licenses it needs.
# Run after editing /shared/lib or rebuilding the core (make core-wasm).
# Pass --check to verify the copies are in sync (CI).
set -eu

cd "$(dirname "$0")/.."

LIBS="apps/pwa/lib/shared apps/extension/lib/shared"
CORES="apps/pwa/vendor/core apps/extension/vendor/core"
NOTICES="LICENSE THIRD_PARTY_NOTICES"

if [ "${1:-}" = "--check" ]; then
    fail=0
    for d in $LIBS; do
        if ! diff -rq shared/lib "$d" >/dev/null 2>&1; then
            echo "OUT OF SYNC: $d (run scripts/sync-extension-lib.sh)" >&2
            fail=1
        fi
    done
    for d in $CORES; do
        if ! diff -rq vendor/core "$d" >/dev/null 2>&1; then
            echo "OUT OF SYNC: $d (run scripts/sync-extension-lib.sh)" >&2
            fail=1
        fi
    done
    for f in $NOTICES; do
        if ! cmp -s "$f" "apps/pwa/$f"; then
            echo "OUT OF SYNC: apps/pwa/$f (run scripts/sync-extension-lib.sh)" >&2
            fail=1
        fi
    done
    exit $fail
fi

for d in $LIBS; do
    rm -rf "$d"
    mkdir -p "$d"
    cp -r shared/lib/. "$d"/
    echo "synced $d"
done

for d in $CORES; do
    rm -rf "$d"
    mkdir -p "$d"
    cp -r vendor/core/. "$d"/
    echo "synced $d"
done

for f in $NOTICES; do
    cp "$f" "apps/pwa/$f"
    echo "synced apps/pwa/$f"
done
