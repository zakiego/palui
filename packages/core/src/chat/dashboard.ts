// Where things live in the dashboard, so the agent can answer "where do I add a
// connector" or "where's the model setting" with a link. Same text on every
// turn of a chat (it sits in the cached system prompt). Keep the paths in sync
// with apps/app/src/routes/_app; dashboard.test.ts checks every one has a route.

// [path, what the owner does there]. `<...>` is filled in by the model.
export const DASHBOARD_PAGES: [string, string][] = [
  ["/app", "Home: setup checklist and usage"],
  ["/app/connectors/new", "Add a connector: channels (WhatsApp, Telegram), models, memory, tools, MCP servers"],
  ["/app/connectors", "Every connector: turn on or off, see which need fixing"],
  ["/app/connectors/<connectorId>", "One connector: its setup, Test, Connect (QR, sign-in), remove"],
  ["/app/agents", "Agents"],
  ["/app/chats?chat=<Chat ID>", "A chat's transcript (/app/chats for all chats)"],
  ["/app/reminders", "Reminders (?tab=upcoming|recurring|done, ?new=true to add one)"],
  ["/app/jobs", "Scheduled jobs"],
  ["/app/flows", "Flows (/app/flows/<flowId> for one)"],
  ["/app/notes", "Notes and standing instructions"],
  ["/app/attachments", "Files and photos sent in chats"],
  ["/app/activity", "Logs (?tab=events), runs (?tab=runs) and token usage (?tab=usage)"],
  ["/app/settings?tab=general", "Bot language, dashboard language, fallback channel"],
  ["/app/settings?tab=models", "Which model answers, and the fallback chain"],
  ["/app/settings?tab=limits", "Budgets: steps and tools per turn, daily token cap, runs per hour"],
  ["/app/settings?tab=security", "Passkeys and backups"],
  ["/app/settings?tab=hooks", "Inbound webhooks"],
]

export function dashboardPrompt(appUrl: string, agentSlug?: string | null): string {
  const base = appUrl.replace(/\/+$/, "")
  const lines = DASHBOARD_PAGES.map(([path, what]) => `- ${base}${path}: ${what}`)
  if (agentSlug && /^[\w-]{1,100}$/.test(agentSlug))
    lines.splice(5, 0, `- ${base}/app/agents/${agentSlug}: Your own settings (?tab=instructions for your persona, ?tab=memory, ?tab=tools, ?tab=chats)`)
  return [
    `Dashboard: the owner's web app at ${base}/app. When they ask where to set up, add, change or check something, answer with the full link to the right page from this list, not just a page name. Never make up a path that isn't here.`,
    ...lines,
  ].join("\n")
}
