// How a run is named wherever runs are listed (Home, Activity).
import type { useLabel, useT } from "@/lib/i18n"

export type RunNames = { job: string | null; chat: string | null; jobGone: boolean; chatGone: boolean; trigger: string }

// Its job, else its chat, else how it was triggered. A job or chat that was
// deleted since says so, never its raw id.
export function runTitle(r: RunNames, t: ReturnType<typeof useT>, label: ReturnType<typeof useLabel>): string {
  if (r.job) return r.job
  if (r.jobGone) return t("runs.deletedJob")
  if (r.chat) return r.chat
  if (r.chatGone) return t("runs.deletedChat")
  return label("run.trigger", r.trigger)
}

// A run's status as a StatusText tone.
export const RUN_TONE: Record<string, "success" | "destructive" | "secondary" | "warning" | "outline"> = {
  ok: "success",
  error: "destructive",
  aborted: "secondary",
  budget: "warning",
  awaiting_approval: "warning",
  running: "outline",
}
