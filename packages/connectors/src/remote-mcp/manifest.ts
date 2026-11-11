import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "remote-mcp",
  kind: "skill",
  name: "Remote MCP setup",
  icon: "server-cog",
  summary: "Let Palui add, list and remove public MCP servers from chat.",
  description:
    "Ask Palui to add a public MCP server (Streamable HTTP, no auth) and it checks the URL, connects once to list the tools, and asks you before saving it as an MCP server connector. Only https URLs on public addresses are accepted, checked again on every request. Servers that need a token are added from the Connectors page, so the token never goes through chat.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the add, list and remove tools. Servers already added keep running.",
  docs: {
    commands: ["tambahin MCP server jadwal-sholat, url https://sholat.zakiego.com/mcp"],
    notes: [
      "Adding and removing a server is destructive: it always asks first, and scheduled jobs can't do it.",
      "A server added from chat gets the id mcp-<name>, external trust, and its tools prefilled from their read-only and destructive hints. Change them on the Connectors page.",
      "From chat Palui can only remove servers it added from chat. Others are removed on the Connectors page.",
    ],
  },
})
