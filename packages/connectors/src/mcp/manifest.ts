import { z } from "zod"
import { defineManifest } from "@palui/core/saklar/define"

const toolClass = z.enum(["read", "write", "destructive"])

export const mcpToolPref = z.object({ enabled: z.boolean(), class: toolClass })

export const mcpConfig = z.object({
  url: z.url({ protocol: /^https?$/ }),
  authMode: z.enum(["none", "bearer", "header"]).default("bearer"),
  // Header name for authMode "header" (e.g. "x-api-key").
  headerName: z.string().trim().min(1).max(100).optional(),
  // Per-tool Switch and class, saved from the listTools wizard step. A tool with
  // no entry is offered to the model as `destructive` (fail closed).
  toolPrefs: z.record(z.string(), mcpToolPref).default({}),
})

export default defineManifest({
  id: "mcp",
  kind: "tools",
  name: "MCP server",
  icon: "plug",
  summary: "Give Palui the tools of an MCP server you run or trust.",
  description:
    "Connects one MCP server over Streamable HTTP. Every tool gets its own Switch and a class: read, write or destructive. Destructive tools always ask before running, and tools from an external server taint the turn so writes ask too.",
  multiple: true,
  trust: "external",
  config: mcpConfig,
  secrets: { token: z.string().min(1).optional() },
  setup: [
    { field: "url", label: "Server URL", placeholder: "https://example.com/api/mcp", help: "Streamable HTTP endpoint. SSE-only servers aren't supported." },
    { field: "authMode", label: "Auth", help: "none, bearer (Authorization: Bearer <token>) or header (a custom header)" },
    { field: "headerName", label: "Header name", optional: true, placeholder: "x-api-key", help: "Only for the custom header mode" },
    { field: "token", label: "Token", secret: true, optional: true },
    { action: "listTools", label: "Pick which tools Palui may use", help: "Read-only tools are prefilled as read, the rest as write or destructive from the server's hints." },
  ],
  presets: [
    { id: "vorneo", label: "Vorneo", config: { url: "https://vorneo.id/api/mcp/", authMode: "bearer" }, secretFields: ["token"] },
    {
      id: "keuangan",
      label: "Keuangan",
      config: { url: "https://keuangan-must-be-done.zakiego.workers.dev/api/mcp", authMode: "bearer" },
      secretFields: ["token"],
    },
    { id: "todomap", label: "Todo Map", config: { url: "https://todo.zakiego.com/api/mcp", authMode: "bearer" }, secretFields: ["token"] },
    { id: "linear", label: "Linear", config: { url: "https://mcp.linear.app/mcp", authMode: "bearer" }, secretFields: ["token"] },
  ],
  removeWarning: "This removes the server and its token from Palui. The server itself isn't touched.",
  docs: {
    notes: [
      "Tools are namespaced <connector id>__<tool>, for example mcp-vorneo__search_listings.",
      "Unknown or unclassified tools are treated as destructive and always ask first.",
    ],
  },
})
