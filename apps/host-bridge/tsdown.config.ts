// Bundles the bridge into one self-contained dist/index.mjs, so the host only
// needs Node to run it (ops/install-host-bridge.sh copies that single file).
import { defineConfig } from "tsdown"

export default defineConfig({
  entry: { index: "src/index.ts" },
  format: "esm",
  platform: "node",
  target: "node24",
  outDir: "dist",
  clean: true,
  dts: false,
  fixedExtension: true,
  sourcemap: false,
  deps: {
    alwaysBundle: () => true,
    onlyBundle: false,
  },
})
