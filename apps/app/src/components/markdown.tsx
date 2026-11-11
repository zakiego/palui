// Renders the safe Markdown subset from `lib/markdown.ts` as React elements.
// No dangerouslySetInnerHTML: model text only ever becomes text nodes, and a
// link only gets an http(s) or mailto href, opened in a new tab with
// rel="noopener noreferrer".
import * as React from "react"
import { parseMarkdown, type Block, type Inline } from "@/lib/markdown"
import { cn } from "@/lib/utils"

export function Markdown({ text, className }: { text: string; className?: string }) {
  const blocks = React.useMemo(() => parseMarkdown(text), [text])
  return <div className={cn("flex min-w-0 flex-col gap-2 break-words", className)}>{blocks.map((b, i) => renderBlock(b, i))}</div>
}

function renderBlock(b: Block, key: number): React.ReactNode {
  switch (b.type) {
    case "p":
      return <p key={key}>{renderInline(b.children)}</p>
    case "heading":
      return (
        <p key={key} className="font-semibold">
          {renderInline(b.children)}
        </p>
      )
    case "code":
      return (
        <pre key={key} className="max-w-full overflow-x-auto rounded-md bg-background/70 p-2.5 font-mono text-xs leading-relaxed">
          <code>{b.text}</code>
        </pre>
      )
    case "list": {
      const items = b.items.map((it, i) => (
        <li key={i} className="ps-0.5">
          {renderInline(it)}
        </li>
      ))
      return b.ordered ? (
        <ol key={key} start={b.start} className="flex list-decimal flex-col gap-1 ps-5 marker:text-muted-foreground">
          {items}
        </ol>
      ) : (
        <ul key={key} className="flex list-disc flex-col gap-1 ps-5 marker:text-muted-foreground">
          {items}
        </ul>
      )
    }
    case "table":
      // A wide table scrolls inside the bubble; the bubble never grows past the transcript.
      return (
        <div key={key} className="max-w-full overflow-x-auto">
          <table className="w-max min-w-full border-collapse text-start text-[0.9375em] tabular-nums">
            <thead>
              <tr>
                {b.head.map((cell, i) => (
                  <th key={i} scope="col" className="border-b border-foreground/20 px-2 py-1 text-start align-bottom font-semibold first:ps-0 last:pe-0">
                    {renderInline(cell)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {b.rows.map((row, r) => (
                <tr key={r}>
                  {row.map((cell, i) => (
                    <td key={i} className="max-w-[24rem] border-b border-foreground/10 px-2 py-1 align-top first:ps-0 last:pe-0">
                      {renderInline(cell)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )
  }
}

function renderInline(nodes: Inline[]): React.ReactNode[] {
  return nodes.map((n, i) => {
    switch (n.type) {
      case "text":
        return n.text
      case "br":
        return <br key={i} />
      case "strong":
        return <strong key={i} className="font-semibold">{renderInline(n.children)}</strong>
      case "em":
        return <em key={i}>{renderInline(n.children)}</em>
      case "code":
        return (
          <code key={i} className="rounded-sm bg-background/70 px-1 py-px font-mono text-[0.875em] break-words">
            {n.text}
          </code>
        )
      case "link":
        return (
          <a key={i} href={n.href} target="_blank" rel="noopener noreferrer" className="underline decoration-from-font underline-offset-2 break-words hover:decoration-2">
            {renderInline(n.children)}
          </a>
        )
    }
  })
}
