// Text from a flow (a message, a prompt, a trigger) with its {{placeholders}}
// set apart, so "🕌 {{trigger.label}}" reads as a fixed emoji plus a value
// filled in at run time instead of one string.
import * as React from "react"
import { cn } from "@/lib/utils"

const PLACEHOLDER = /(\{\{[^{}]*\}\})/

export function splitTemplate(text: string): { text: string; placeholder: boolean }[] {
  return text
    .split(PLACEHOLDER)
    .filter((part) => part !== "")
    .map((part) => ({ text: part, placeholder: PLACEHOLDER.test(part) }))
}

export function TemplateText({ text, className }: { text: string; className?: string }) {
  if (!text.includes("{{")) return <>{text}</>
  return (
    <>
      {splitTemplate(text).map((part, i) =>
        part.placeholder ? (
          <span
            key={i}
            className={cn("rounded-sm bg-muted px-1 py-px font-mono text-[0.9em] text-foreground ring-1 ring-border ring-inset box-decoration-clone", className)}
          >
            {part.text}
          </span>
        ) : (
          <React.Fragment key={i}>{part.text}</React.Fragment>
        ),
      )}
    </>
  )
}
