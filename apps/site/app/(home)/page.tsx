import { ArrowRight } from "lucide-react"
import type { Metadata, Viewport } from "next"
import Link from "next/link"
import { MascotHero } from "@/components/landing/mascot-hero"
import { appName, homeTitle, siteDescription, siteImage, siteUrl } from "@/lib/shared"

export const metadata: Metadata = {
  title: { absolute: homeTitle },
  alternates: { canonical: "/" },
  // Page-level openGraph replaces the layout's, so it's complete here.
  openGraph: { type: "website", siteName: appName, locale: "en_US", url: "/", title: homeTitle, description: siteDescription, images: [siteImage] },
  twitter: { card: "summary_large_image", title: homeTitle, description: siteDescription, images: [siteImage] },
}

// The landing is dark whatever the system theme, so the browser chrome is too.
export const viewport: Viewport = { themeColor: "#0a0a0a", colorScheme: "dark" }

const jsonLd = {
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  name: appName,
  description: siteDescription,
  url: siteUrl,
  applicationCategory: "CommunicationApplication",
  operatingSystem: "Linux (Docker)",
  author: { "@type": "Person", name: "Zaki", url: "https://zakiego.com" },
}

// One headline, one sentence, one call to action, one visual (TASTE.md).
// Everything that explains lives in the docs.
export default function HomePage() {
  return (
    <main className="lp-main">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }} />
      <div className="lp-intro">
        <h1 className="lp-h1">An agent friend you host yourself</h1>
        <p className="lp-lede">A simple, friendly agent that keeps you company</p>
        <div className="lp-cta-row">
          <Link href="/docs" className="lp-cta">
            Read the docs
            <ArrowRight aria-hidden="true" />
          </Link>
        </div>
      </div>
      <MascotHero />
    </main>
  )
}
