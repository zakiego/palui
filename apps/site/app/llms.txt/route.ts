import { docsLlms } from "@/lib/source"

// llms.txt: an index of every docs page for agents. Each page's own Markdown
// is at /llms.mdx/docs/<slug>/content.md.
export const revalidate = false

export function GET() {
  return new Response(docsLlms.index(), {
    headers: { "Content-Type": "text/markdown; charset=utf-8" },
  })
}
