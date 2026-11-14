// The dashboard's entry: a client-only single-page app under /app. The Go
// server answers /api and /health and serves dist/index.html for every /app
// path, so routing happens here.
import "@/styles/app.css"
import { RouterProvider } from "@tanstack/react-router"
import { createRoot } from "react-dom/client"
import { redirectToLoginOnUnauthorized } from "@/lib/session-redirect"
import { getRouter } from "./router"

const router = getRouter()
redirectToLoginOnUnauthorized(router)

createRoot(document.getElementById("root")!).render(<RouterProvider router={router} />)
