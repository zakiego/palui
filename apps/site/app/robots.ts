import type { MetadataRoute } from "next"
import { siteUrl } from "@/lib/shared"

export const dynamic = "force-static"

export default function robots(): MetadataRoute.Robots {
  return {
    // /api and /health live on the same host but aren't pages. The dashboard
    // (/app) is left crawlable on purpose: every page there sends `noindex`,
    // and a crawler that's blocked from fetching it never sees that and can
    // list the URL anyway.
    rules: [{ userAgent: "*", allow: "/", disallow: ["/api/", "/health"] }],
    sitemap: `${siteUrl}/sitemap.xml`,
  }
}
