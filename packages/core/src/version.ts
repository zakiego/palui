// The Palui version, read from the root package.json (the only place it lives).
// Vite and tsdown inline the JSON at build time, so a built app or runtime
// reports the version it was built from. Bumped by the nightly release PR (palui-release skill).
import pkg from "../../../package.json" with { type: "json" }

export const PALUI_VERSION: string = pkg.version
