// GET /health, no auth. See src/server/health.ts for what changes the status code.
import { createFileRoute } from "@tanstack/react-router"
import { getDb } from "@/server/db"
import { health } from "@/server/health"
import { runtimeLive } from "@/server/runtime"

export const Route = createFileRoute("/health")({
  server: {
    handlers: {
      GET: () => health(getDb(), () => runtimeLive()),
    },
  },
})
