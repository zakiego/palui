import { Children, type ComponentProps } from "react"
import { CHANGE_TYPES, type ChangeType } from "@/lib/changelog"

const PREFIX = new RegExp(`^(${CHANGE_TYPES.join("|")}): `)

// The changelog's <li>. An entry is written `- Fixed: Something.` so the
// markdown stays readable as plain text; here the prefix becomes a colored
// dot and a quiet label in its own column, and the entry's text lines up
// after it. Anything else (the intro list, Breaking sub-steps) renders as a
// normal list item.
export function ChangelogItem({ children, className, ...props }: ComponentProps<"li">) {
  const parts = Children.toArray(children)
  const first = parts[0]
  const match = typeof first === "string" ? PREFIX.exec(first) : null
  if (!match) return <li className={className} {...props}>{children}</li>

  const type = match[1] as ChangeType
  const rest = [(first as string).slice(match[0].length), ...parts.slice(1)]
  return (
    <li className="cl-item" data-type={type.toLowerCase()} {...props}>
      <span className="cl-type">{type}</span>
      <span>{rest}</span>
    </li>
  )
}
