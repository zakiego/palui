import Link from "next/link"
import { MARK_TILE, PaluiFace } from "@/components/palui-mark"
import { appName } from "@/lib/shared"
import "@/components/landing/landing.css"

// The landing keeps its own chrome instead of Fumadocs' HomeLayout: a glass
// bar that stays at the top with the brand and one link to the docs, and a
// one-line footer. It's always dark (`dark` keeps any shared token on the dark
// ramp, landing.css does the rest).
export default function Layout({ children }: LayoutProps<"/">) {
  return (
    <div className="dark lp-root">
      <header className="lp-header">
        <div className="lp-header-in">
          <Link href="/" className="lp-brand">
            <svg viewBox="0 0 32 32" aria-hidden="true" className="lp-mark">
              <path d={MARK_TILE} className="lp-mark-tile" />
              <PaluiFace className="lp-mark-face" />
            </svg>
            {appName}
          </Link>
          <Link href="/docs" className="lp-nav">
            Docs
          </Link>
        </div>
      </header>
      {children}
      <footer className="lp-footer">
        palui.zakiego.com <span aria-hidden="true">·</span> built by <a href="https://zakiego.com">Zakiego</a> <span aria-hidden="true">·</span>{" "}
        <Link href="/brand">Brand</Link>
      </footer>
    </div>
  )
}
