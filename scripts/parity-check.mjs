// Dev-only Go↔WASM parity check (run: make parity). Not a runtime dependency.
//
// Loads the vendored astra-core.wasm under node, feeds it every request
// fixture in core/testdata/parity/, and byte-compares the responses against
// the golden files that `go test ./core -run Parity` asserts natively.
// Identical output here proves the browser surfaces get exactly what the
// CLI computes.
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

// wasm_exec.js is a classic script that installs globalThis.Go.
const wasmExecSource = readFileSync(join(root, "vendor/core/wasm_exec.js"), "utf8");
new Function(wasmExecSource)();

const go = new Go();
const { instance } = await WebAssembly.instantiate(
  readFileSync(join(root, "vendor/core/astra-core.wasm")),
  go.importObject,
);
go.run(instance); // resolves only on exit; astraCore is installed synchronously at startup

// Give the Go runtime a tick to install the global.
await new Promise((r) => setTimeout(r, 50));
if (typeof globalThis.astraCore?.process !== "function") {
  console.error("FAIL: astraCore.process not installed by the WASM module");
  process.exit(1);
}

const dir = join(root, "core/testdata/parity");
let failures = 0;
for (const name of readdirSync(dir)) {
  if (!name.endsWith(".json") || name.endsWith(".golden.json")) continue;
  const req = readFileSync(join(dir, name), "utf8");
  const golden = JSON.parse(
    readFileSync(join(dir, name.replace(/\.json$/, ".golden.json")), "utf8"),
  );
  const got = JSON.parse(globalThis.astraCore.process(req));
  const gotCanon = JSON.stringify(got);
  const wantCanon = JSON.stringify(golden);
  if (gotCanon === wantCanon) {
    console.log(`ok   ${name}`);
  } else {
    failures++;
    console.error(`FAIL ${name}\n  got:  ${gotCanon.slice(0, 400)}\n  want: ${wantCanon.slice(0, 400)}`);
  }
}
process.exit(failures ? 1 : 0);
