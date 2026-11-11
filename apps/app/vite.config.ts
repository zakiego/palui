import tailwindcss from "@tailwindcss/vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import { nitro } from "nitro/vite"
import { defineConfig } from "vite"

const APP_BASE = "/app"

export default defineConfig({
  resolve: { tsconfigPaths: true, dedupe: ["react", "react-dom"] },
  server: { port: 4890 },
  plugins: [
    tailwindcss(),
    // The dashboard lives under /app. The REST API (/api), Better Auth
    // (/api/auth) and /health stay at the root; see src/lib/app-path.ts.
    tanstackStart({ router: { basepath: APP_BASE } }),
    nitro({
      preset: "node-server",
      // Built assets go to .output/public/app/assets, so they're served at
      // /app/assets with the dashboard. This instead of Vite's `base`: Nitro
      // takes `base` as its own baseURL and then only answers under /app,
      // redirecting the API there too.
      buildAssetsDir: `${APP_BASE.slice(1)}/assets`,
    }),
    viteReact(),
  ],
})
