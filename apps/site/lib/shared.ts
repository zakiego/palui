import { createGetUrl } from "fumadocs-core/source"

export const appName = "Palui Agent"
export const shortName = "Palui"
export const siteUrl = "https://palui.zakiego.com"
export const homeTitle = `${appName}: an agent friend you host yourself`
export const siteDescription =
  "A personal AI agent you host yourself. It remembers what you tell it, reminds you on time, and talks to you on WhatsApp, Telegram or the web."
export const siteImage = {
  url: "/og.png?v=2",
  width: 1200,
  height: 630,
  alt: "Palui Agent: an agent friend you host yourself",
}
export const docsRoute = "/docs"
export const docsImageRoute = "/og/docs"
export const docsContentRoute = "/llms.mdx/docs"
// Not /api/search: /api belongs to the app on this hostname (apps/edge).
export const searchIndexRoute = "/search.json"

const getContentUrl = createGetUrl(docsContentRoute)

export function getPageMarkdownUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [...page.slugs, "content.md"]
  return { segments, url: getContentUrl(segments, page.locale) }
}

const getImageUrl = createGetUrl(docsImageRoute)

export function getPageImageUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [...page.slugs, "image.png"]
  return { segments, url: getImageUrl(segments, page.locale) }
}
