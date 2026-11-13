import type { Metadata, Viewport } from "next"
import Link from "next/link"
import { Fragment } from "react"
import { Bento } from "@/components/landing/bento"
import { HeroStage } from "@/components/landing/hero-stage"
import { appName, homeTitle, siteDescription, siteImage, siteUrl } from "@/lib/shared"

export const metadata: Metadata = {
  title: { absolute: homeTitle },
  alternates: { canonical: "/" },
  // Page-level openGraph replaces the layout's, so it's complete here.
  openGraph: { type: "website", siteName: appName, locale: "en_US", url: "/", title: homeTitle, description: siteDescription, images: [siteImage] },
  twitter: { card: "summary_large_image", title: homeTitle, description: siteDescription, images: [siteImage] },
}

// The landing is dark whatever the system theme, so the browser chrome is too.
export const viewport: Viewport = { themeColor: "#08090a", colorScheme: "dark" }

const jsonLd = {
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  name: appName,
  description: siteDescription,
  url: siteUrl,
  image: `${siteUrl}${siteImage.url}`,
  applicationCategory: "CommunicationApplication",
  operatingSystem: "Linux (Docker)",
  author: { "@type": "Person", name: "Zaki", url: "https://zakiego.com" },
}

// Each word of the headline comes into focus on load, 40 ms after the last.
const HEADLINE = "An agent friend you host yourself".split(" ")

// The page (DESIGN.md, 2026-11-13): a hero where Palui acts out what you ask,
// one box per thing it does with a live demo in each, and a closing call to
// action, split by hairlines. Neutrals only. Anything that explains lives in
// the docs.
export default function HomePage() {
  return (
    <main className="lp-main">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }} />

      <section className="lp-wrap lp-hero">
        <div className="lp-intro">
          <h1 className="lp-h1">
            {HEADLINE.map((word, i) => (
              <Fragment key={word}>
                {i > 0 ? " " : null}
                <span className="lp-word" style={{ animationDelay: `${i * 40}ms` }}>
                  {word}
                </span>
              </Fragment>
            ))}
          </h1>
          <div className="lp-intro-side">
            <p className="lp-lede">It remembers things, reminds you on time and runs your tools</p>
            <div className="lp-cta-row">
              <Link href="/docs" className="lp-cta">
                Read the docs
              </Link>
              <Link href="/docs/getting-started" className="lp-cta lp-cta-ghost">
                Get started
              </Link>
            </div>
          </div>
        </div>
        <HeroStage />
      </section>

      <hr className="lp-rule" />

      <section className="lp-wrap lp-section" aria-labelledby="does">
        <h2 id="does" className="lp-h2">
          What it does <span className="lp-h2-dim">for you</span>
        </h2>
        <Bento />
      </section>

      <hr className="lp-rule" />

      <section className="lp-wrap lp-section lp-close" aria-labelledby="home">
        <h2 id="home" className="lp-h2 lp-close-title">
          Bring Palui home
        </h2>
        <div className="lp-cta-row">
          <Link href="/docs" className="lp-cta lp-cta-big">
            Read the docs
          </Link>
          <Link href="/docs/getting-started" className="lp-cta lp-cta-ghost lp-cta-big">
            Get started
          </Link>
        </div>
      </section>

      <hr className="lp-rule" />
    </main>
  )
}
