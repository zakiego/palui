// A turn's tool calls as a collapsible trace: name, class, outcome and time on
// one line, args and result inside. Shared by the chat transcript and the run page.
import * as React from "react"
import { WrenchIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { Trace, TraceCollapsible, TraceCollapsibleContent, TraceCollapsibleTrigger, TraceDescription, TraceIcon, TraceLine, TraceTitle } from "@/components/ui/trace"
import { duration } from "@/lib/format"
import { useLabel, useT } from "@/lib/i18n"

export type TraceTool = { id: string; tool: string; class: string; status: "running" | "ok" | "error" | "denied" | "pending"; ms?: number; preview?: string; args?: string }

export const CLASS_TONE: Record<string, "secondary" | "warning" | "destructive"> = { read: "secondary", write: "warning", destructive: "destructive" }

// Tool calls shown before the rest fold behind a button. A turn can make
// dozens, and thirty lines above a reply bury it.
const TRACE_LIMIT = 5

const PRE = "max-h-48 overflow-auto rounded-md bg-code p-2 font-mono text-xs break-all whitespace-pre-wrap"

export function ToolTrace({ tools, limit = TRACE_LIMIT, className }: { tools: TraceTool[]; limit?: number; className?: string }) {
  const t = useT()
  const label = useLabel()
  const [showAll, setShowAll] = React.useState(false)
  const shown = showAll || tools.length <= limit + 1 ? tools : tools.slice(0, limit)
  const hidden = tools.length - shown.length
  const hiddenFailed = tools.slice(shown.length).filter((x) => x.status === "error").length
  return (
    <Trace className={className}>
      {shown.map((x) => (
        <TraceCollapsible key={x.id}>
          <TraceCollapsibleTrigger>
            <TraceLine status={x.status === "running" ? "running" : x.status === "error" ? "error" : undefined}>
              <TraceIcon>{x.status === "running" ? <Spinner /> : <WrenchIcon />}</TraceIcon>
              <TraceTitle className="font-mono text-caption" title={x.tool}>
                {x.tool}
              </TraceTitle>
              <StatusText tone={CLASS_TONE[x.class] ?? "destructive"} icon={null}>{label("tool.class", x.class)}</StatusText>
              {/* A word next to the color, so a failed call doesn't rely on red alone. */}
              {x.status === "error" && <StatusText tone="destructive">{t("tool.failed")}</StatusText>}
              {x.status === "denied" && <StatusText tone="outline">{t("tool.denied")}</StatusText>}
              {x.ms != null && <TraceDescription>{duration(x.ms)}</TraceDescription>}
            </TraceLine>
          </TraceCollapsibleTrigger>
          <TraceCollapsibleContent className="flex flex-col gap-2">
            {/* The trigger truncates the name; here it's whole. */}
            <span className="font-mono text-caption break-all">{x.tool}</span>
            {x.args && <pre className={PRE}>{x.args}</pre>}
            {x.preview ? <pre className={PRE}>{x.preview}</pre> : <span className="text-caption">{x.status === "running" || x.status === "pending" ? t("chats.noResult") : t("tool.noResult")}</span>}
          </TraceCollapsibleContent>
        </TraceCollapsible>
      ))}
      {hidden > 0 && (
        <Button variant="ghost" size="sm" className="self-start" onClick={() => setShowAll(true)}>
          {hiddenFailed > 0 ? t("chats.moreToolsFailed", { n: hidden, failed: hiddenFailed }) : t("chats.moreTools", { n: hidden })}
        </Button>
      )}
    </Trace>
  )
}
