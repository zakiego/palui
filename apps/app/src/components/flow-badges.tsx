// Flow status bits shared by the Flows pages and the run page: a colored icon
// plus text (StatusText), never a filled pill.
import { StatusText } from "@/components/ui/status-text"
import { useT } from "@/lib/i18n"

export const FLOW_RUN_TONE: Record<string, "success" | "destructive" | "secondary" | "warning" | "outline"> = {
  done: "success",
  failed: "destructive",
  cancelled: "secondary",
  skipped: "secondary",
  awaiting_approval: "warning",
  waiting: "outline",
  running: "outline",
  queued: "outline",
}

type FlowFlags = { enabled: boolean; failures: number; approval: "approved" | "waiting" | "not_needed" }

export function FlowBadges({ flow }: { flow: FlowFlags }) {
  const t = useT()
  return (
    <>
      {!flow.enabled && <StatusText tone="secondary">{t("flows.paused")}</StatusText>}
      {flow.enabled && flow.failures > 0 && <StatusText tone="destructive">{t("flows.failing")}</StatusText>}
      {flow.approval === "waiting" && <StatusText tone="warning">{t("flows.waitingApproval")}</StatusText>}
    </>
  )
}

