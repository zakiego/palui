import { defineConfig } from "vitest/config"

export default defineConfig({
  resolve: { tsconfigPaths: true },
  test: {
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
    environment: "node",
    setupFiles: ["./src/test/setup.ts"],
    testTimeout: 60_000,
    hookTimeout: 120_000,
  },
})
