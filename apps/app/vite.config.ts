import tailwindcss from "@tailwindcss/vite"
import { tanstackRouter } from "@tanstack/router-plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { defineConfig, loadEnv } from "vite"

// The dashboard is a static single-page app under /app that the Go server
// (apps/server) serves from its embedded build: `vite build` writes index.html,
// favicon.svg and hashed files under assets/ to dist/, which the Go build
// copies into apps/server/internal/web/dist. The API (/api) and /health stay
// at the root and belong to the Go server; `vite dev` proxies them there.
const APP_BASE = "/app/"

// Where `vite dev` sends /api and /health: a `palui serve` on its default port.
// Override with PALUI_API_URL (for example `PALUI_API_URL=http://localhost:4895 pnpm dev`).
const DEFAULT_API_URL = "http://localhost:3000"

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "PALUI_")
  const api = env.PALUI_API_URL || DEFAULT_API_URL
  // Keep the Host header (changeOrigin false): the Go server checks a write's
  // Origin against APP_URL, and its dev default is this server's address.
  const proxy = { target: api, changeOrigin: false }
  return {
    base: APP_BASE,
    // public/app/favicon.svg is written by apps/site/scripts/brand-icons.mjs;
    // it lands at dist/favicon.svg, served as /app/favicon.svg.
    publicDir: "public/app",
    resolve: { tsconfigPaths: true, dedupe: ["react", "react-dom"] },
    server: {
      port: 4890,
      strictPort: true,
      proxy: {
        "/api": { ...proxy, ws: false },
        "/health": proxy,
      },
    },
    preview: { port: 4890 },
    build: {
      outDir: "dist",
      assetsDir: "assets",
      emptyOutDir: true,
    },
    plugins: [
      // File routes from src/routes.
      tanstackRouter({ target: "react", autoCodeSplitting: true }),
      viteReact(),
      tailwindcss(),
    ],
  }
})
