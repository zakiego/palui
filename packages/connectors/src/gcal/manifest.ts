import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "gcal",
  kind: "tools",
  name: "Google Calendar",
  icon: "calendar",
  summary: "Let Palui read your Google Calendar. Read only.",
  description:
    "Palui can look at your events (calendar.readonly scope) but never create, change or delete them. You bring your own Google OAuth client: create a Web application client in Google Cloud Console and add the redirect URI shown in the setup.",
  config: z.object({
    calendarIds: z.array(z.string().trim().min(1)).min(1).default(["primary"]),
    // Learned after sign-in (the primary calendar id is the account email).
    email: z.string().optional(),
  }),
  secrets: {
    clientId: z.string().trim().regex(/\.apps\.googleusercontent\.com$/, "Google client ids end with .apps.googleusercontent.com"),
    clientSecret: z.string().trim().min(10),
    refreshToken: z.string().min(10).optional(),
  },
  setup: [
    {
      field: "clientId",
      label: "OAuth client ID",
      placeholder: "1234-abc.apps.googleusercontent.com",
      help: "In Google Cloud Console, Credentials, make a Web application client with this redirect URI, then paste its ID.",
      command: "{appUrl}/api/oauth/google/callback",
    },
    { field: "clientSecret", label: "OAuth client secret", secret: true },
    { action: "oauth", label: "Connect with Google" },
  ],
  trust: "own",
  removeWarning: "This deletes the Google tokens from Palui. To revoke access fully, also remove Palui at myaccount.google.com/permissions.",
  docs: {
    notes: [
      "Tool: lihat_kalender (read). Times are shown in your timezone.",
      "Anyone can put text in your calendar by sending an invite, so calendar results taint the turn: any write after them asks you first.",
    ],
  },
})
