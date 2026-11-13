import { DocsLayout } from "fumadocs-ui/layouts/docs"
import type { Viewport } from "next"
import { baseOptions, dashboardLink } from "@/lib/layout.shared"
import { docsFontVars } from "@/lib/docs-fonts"
import { source } from "@/lib/source"
import "./docs.css"

// The docs open dark on the dashboard's chrome black (docs.css).
export const viewport: Viewport = {
  themeColor: "#08090a",
}

export default function Layout({ children }: LayoutProps<"/docs">) {
  // The sidebar already is the docs, so it only keeps the dashboard link.
  return (
    <>
      <style href="docs-fonts" precedence="default">
        {docsFontVars}
      </style>
      <DocsLayout tree={source.getPageTree()} {...baseOptions()} links={[dashboardLink]}>
        {children}
      </DocsLayout>
    </>
  )
}
