import { z } from "zod"
import { defineManifest } from "../define"

// The form's copy of the server's rules (internal/connectors/ics).
export function feedUrl(s: string): boolean {
  try {
    const u = new URL(s.trim())
    return u.host !== "" && ["https:", "http:", "webcal:", "webcals:"].includes(u.protocol)
  } catch {
    return false
  }
}

export function emailList(s: string): boolean {
  return s
    .split(",")
    .map((e) => e.trim())
    .every((e) => e === "" || (e.includes("@") && !/[\s<>]/.test(e)))
}

export const icsConfig = z.object({
  emails: z.string().trim().max(500).refine(emailList, "Use email addresses separated by commas").optional(),
})

export default defineManifest({
  id: "ics",
  kind: "tools",
  name: "Calendar feed",
  icon: "calendar",
  summary: "Read any calendar from its secret iCal link.",
  multiple: true,
  trust: "external",
  config: icsConfig,
  secrets: {
    url: z.string().trim().min(10).max(2000).refine(feedUrl, "Use the calendar's https:// or webcal:// address"),
  },
  setup: [
    {
      field: "url",
      label: "iCal address",
      secret: true,
      placeholder: "https://calendar.google.com/calendar/ical/…/basic.ics",
      help: "The calendar's private iCal link. Google: Settings, your calendar, Secret address in iCal format. iCloud: share the calendar as public. Outlook: Publish a calendar, ICS. Fastmail and Nextcloud: the subscription link.",
    },
    { field: "emails", label: "Your email addresses", optional: true, placeholder: "you@example.com", help: "Separated by commas. Invites you declined are marked declined." },
  ],
  pauseNote: "While paused, agents can't read this calendar.",
  removeWarning: "This removes the link from Palui. The calendar isn't touched.",
  docs: {
    notes: [
      "Read only: one tool, get_events, lists the events in a time range, recurring ones expanded.",
      "The feed is read at most every 5 minutes. Most services refresh their iCal link every few minutes to a few hours.",
      "Event titles and descriptions come from whoever sent the invite, so they're treated as outside text.",
    ],
  },
})
