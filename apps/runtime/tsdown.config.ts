// Bundles the runtime into dist/index.mjs. Workspace packages are TS source, so
// they (and pure-JS dependencies) are bundled. Native or heavy packages stay
// external and must be in this package's dependencies, so `pnpm deploy` puts
// them in node_modules next to dist/:
//   better-sqlite3  native addon
//   baileys         WhatsApp (optional native deps: sharp, jimp, link-preview-js)
import { defineConfig } from "tsdown"

const EXTERNAL = ["better-sqlite3", "baileys", "sharp", "jimp", "link-preview-js", "audio-decode", "@img/sharp-*"]

function isExternal(id: string): boolean {
  return EXTERNAL.some((p) => (p.endsWith("*") ? id.startsWith(p.slice(0, -1)) : id === p || id.startsWith(`${p}/`)))
}

export default defineConfig({
  entry: { index: "src/index.ts" },
  format: "esm",
  platform: "node",
  target: "node24",
  outDir: "dist",
  clean: true,
  dts: false,
  fixedExtension: true,
  sourcemap: true,
  deps: {
    neverBundle: (id: string) => isExternal(id),
    alwaysBundle: (id: string) => !isExternal(id),
    onlyBundle: false,
  },
})
