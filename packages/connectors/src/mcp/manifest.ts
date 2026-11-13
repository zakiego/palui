import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

const toolClass = z.enum(["read", "write", "destructive"])

// flowApproval (RFC flows 7.2): how a flow's write step on this tool is approved,
// once per flow version (default) or on every run (a tool whose effects reach
// other people). Read at run time, so tightening it applies to existing flows.
export const mcpToolPref = z.object({ enabled: z.boolean(), class: toolClass, flowApproval: z.enum(["version", "every_run"]).optional() })

export const mcpConfig = z.object({
  url: z.url({ protocol: /^https?$/ }),
  authMode: z.enum(["none", "bearer", "header"]).default("bearer"),
  // Header name for authMode "header" (e.g. "x-api-key").
  headerName: z.string().trim().min(1).max(100).optional(),
  // Per-tool Switch and class, saved from the listTools wizard step. A tool with
  // no entry is offered to the model as `destructive` (fail closed).
  toolPrefs: z.record(z.string(), mcpToolPref).default({}),
  // Set when Palui added the server from chat (the remote-mcp skill). Such a
  // server only ever talks to public addresses, checked on every request.
  addedFromChat: z.boolean().optional(),
})

export default defineManifest({
  id: "mcp",
  kind: "tools",
  name: "MCP server",
  icon: "plug",
  summary: "Tools from an MCP server you trust.",
  multiple: true,
  trust: "external",
  config: mcpConfig,
  secrets: { token: z.string().min(1).optional() },
  setup: [
    { field: "url", label: "Server URL", placeholder: "https://example.com/api/mcp", help: "Streamable HTTP only, not SSE." },
    { field: "authMode", label: "Auth", askFirst: true, help: "bearer: Authorization header. header: a header you name." },
    { field: "headerName", label: "Header name", askFirst: true, placeholder: "x-api-key", showIf: { field: "authMode", in: ["header"] } },
    { field: "token", label: "Token", secret: true, optional: true, askFirst: true, showIf: { field: "authMode", in: ["bearer", "header"] } },
    { action: "listTools", label: "Pick which tools Palui may use" },
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
    // apps/host-bridge on the Docker host, installed by ops/install-host-bridge.sh.
    // Trust is own: the owner writes every command, and the output is their own machine's.
    {
      id: "host",
      label: "Host commands",
      config: { url: "http://host.docker.internal:4893/mcp", authMode: "bearer" },
      secretFields: ["token"],
      trust: "own",
    },
  ],
  removeWarning: "This removes the token from Palui. The server isn't touched.",
  docs: {
    notes: [
      "Tools are namespaced <connector id>__<tool>, for example mcp-vorneo__search_listings.",
      "Unknown or unclassified tools are treated as destructive and always ask first.",
    ],
  },
})
