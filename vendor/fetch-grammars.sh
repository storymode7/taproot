#!/usr/bin/env bash
# vendor/fetch-grammars.sh
#
# Downloads pre-built tree-sitter grammar WASM files from their respective
# GitHub release pages. Verifies SHA256 checksums after download.
#
# Run once to populate vendor/grammars/. Re-running is idempotent: files
# already present with matching checksums are skipped.
#
# NO npm, NO compilers. Just curl + sha256sum.
#
# To update a grammar: bump the URL and checksum below, re-run this script.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GRAMMARS_DIR="$SCRIPT_DIR/grammars"
CHECKSUMS_FILE="$SCRIPT_DIR/checksums.txt"

mkdir -p "$GRAMMARS_DIR"

# ---------------------------------------------------------------------------
# Grammar definitions
# Format: FILENAME|URL|SHA256
#
# Sources:
#   tree-sitter runtime WASM:
#     https://github.com/tree-sitter/tree-sitter/releases
#   Grammar WASMs:
#     Published by tree-sitter/web-tree-sitter as part of each grammar's
#     GitHub release, or available from the grammar's own GitHub releases.
#
# IMPORTANT: When updating, verify the SHA256 of the file you received
#   before committing the new checksum here.
# ---------------------------------------------------------------------------
declare -a GRAMMARS=(
  # tree-sitter runtime WASM (extracted from web-tree-sitter.tar.gz)
  # Source: https://github.com/tree-sitter/tree-sitter/releases/tag/v0.26.9 (released 2026-05-19)
  # This entry is handled specially below (tarball extraction).
  # Direct WASM SHA: 1ed02fe75a60642bea084670e33267795aab26c19012805b9fb513ab458680ee
  "web-tree-sitter.js|TARBALL:https://github.com/tree-sitter/tree-sitter/releases/download/v0.26.9/web-tree-sitter.tar.gz:web-tree-sitter.js|0c868236a47296b4ff3c1570f20e0899e4a784ff6e5cd7bfc9c3a55225463e4a"
  "tree-sitter.wasm|TARBALL:https://github.com/tree-sitter/tree-sitter/releases/download/v0.26.9/web-tree-sitter.tar.gz:web-tree-sitter.wasm|1ed02fe75a60642bea084670e33267795aab26c19012805b9fb513ab458680ee"

  # JavaScript grammar — v0.25.0 (released 2025-09-01)
  # Source: https://github.com/tree-sitter/tree-sitter-javascript/releases/tag/v0.25.0
  "tree-sitter-javascript.wasm|https://github.com/tree-sitter/tree-sitter-javascript/releases/download/v0.25.0/tree-sitter-javascript.wasm|5fb488d0cabb4775a594bab85682de5ad6ce83c0d6ac997a9f82dd084d571240"

  # TypeScript grammar — v0.23.2 (released 2024-11-11)
  # Source: https://github.com/tree-sitter/tree-sitter-typescript/releases/tag/v0.23.2
  "tree-sitter-typescript.wasm|https://github.com/tree-sitter/tree-sitter-typescript/releases/download/v0.23.2/tree-sitter-typescript.wasm|778025db5a8be0e70f8ccc3671e486dfeddd048c25d9e8a70c26de2e1bf6f97d"

  # Python grammar — v0.25.0 (released 2025-09-11)
  # Source: https://github.com/tree-sitter/tree-sitter-python/releases/tag/v0.25.0
  "tree-sitter-python.wasm|https://github.com/tree-sitter/tree-sitter-python/releases/download/v0.25.0/tree-sitter-python.wasm|16108b50df4ee9a30168794252ab55e7c93bfc5765d7fa0aa3e335752c515f47"

  # Go grammar — v0.25.0 (released 2025-08-29)
  # Source: https://github.com/tree-sitter/tree-sitter-go/releases/tag/v0.25.0
  "tree-sitter-go.wasm|https://github.com/tree-sitter/tree-sitter-go/releases/download/v0.25.0/tree-sitter-go.wasm|9504573f352b20be7f2f1911754d710622aedc15afff16d5ed8fb5645681aee7"

  # Java grammar — v0.23.5 (released 2024-12-21)
  # Source: https://github.com/tree-sitter/tree-sitter-java/releases/tag/v0.23.5
  "tree-sitter-java.wasm|https://github.com/tree-sitter/tree-sitter-java/releases/download/v0.23.5/tree-sitter-java.wasm|4fdeac4ca6ca089f06c6f7e562abcac1733cd465728cc7031ebb73c2019122c4"

  # Rust grammar — v0.24.2 (released 2026-03-27)
  # Source: https://github.com/tree-sitter/tree-sitter-rust/releases/tag/v0.24.2
  "tree-sitter-rust.wasm|https://github.com/tree-sitter/tree-sitter-rust/releases/download/v0.24.2/tree-sitter-rust.wasm|24c89bd9252255e4aebbcbd7d2d308bd92c86dd95a130fdc80efa49577b8d738"
)

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
sha256_of() {
  if command -v sha256sum &>/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# ---------------------------------------------------------------------------
# Main loop
# ---------------------------------------------------------------------------
> "$CHECKSUMS_FILE.tmp"

all_ok=true

for entry in "${GRAMMARS[@]}"; do
  IFS='|' read -r filename url expected_sha256 <<< "$entry"
  dest="$GRAMMARS_DIR/$filename"

  if [[ -f "$dest" ]]; then
    actual="$(sha256_of "$dest")"
    if [[ "$actual" == "$expected_sha256" ]]; then
      echo "✓  $filename (already present, checksum OK)"
      echo "$expected_sha256  $filename" >> "$CHECKSUMS_FILE.tmp"
      continue
    else
      echo "⚠  $filename checksum mismatch — re-downloading"
      rm "$dest"
    fi
  fi

  # Handle TARBALL: entries (download tar, extract one file)
  if [[ "$url" == TARBALL:* ]]; then
    tarball_url="${url#TARBALL:}"
    inner_path="${tarball_url##*:}"
    tarball_url="${tarball_url%%:*}"
    tmp_tar="$(mktemp /tmp/astra-grammar-XXXXXX.tar.gz)"
    echo "↓  Downloading tarball for $filename from $tarball_url"
    curl --fail --silent --show-error --location --output "$tmp_tar" "$tarball_url"
    tar -xzf "$tmp_tar" -C "$(dirname "$dest")" --strip-components=0 "./$inner_path" 2>/dev/null || \
      tar -xzf "$tmp_tar" -O "./$inner_path" > "$dest" 2>/dev/null || \
      tar -xzf "$tmp_tar" --wildcards "*/$inner_path" -O > "$dest"
    # Rename if extracted with different name
    extracted="$(dirname "$dest")/$inner_path"
    if [[ -f "$extracted" && "$extracted" != "$dest" ]]; then
      mv "$extracted" "$dest"
    fi
    rm -f "$tmp_tar"
  else
    echo "↓  Downloading $filename from $url"
    curl --fail --silent --show-error --location --output "$dest" "$url"
  fi

  actual="$(sha256_of "$dest")"
  if [[ "$actual" != "$expected_sha256" ]]; then
    echo "❌ CHECKSUM FAILED for $filename"
    echo "   expected: $expected_sha256"
    echo "   got:      $actual"
    rm -f "$dest"
    all_ok=false
    continue
  fi

  echo "✓  $filename (downloaded, checksum OK)"
  echo "$expected_sha256  $filename" >> "$CHECKSUMS_FILE.tmp"
done

mv "$CHECKSUMS_FILE.tmp" "$CHECKSUMS_FILE"

echo ""
if $all_ok; then
  echo "All grammar WASM files present and verified."
else
  echo "Some files need attention (see ⚠ / ❌ above)."
  echo "Check vendor/fetch-grammars.sh and update PLACEHOLDER_VERIFY_BEFORE_USE entries"
  echo "with the actual SHA256 of each file after manual verification."
  exit 1
fi
