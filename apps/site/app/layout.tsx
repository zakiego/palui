import type { Metadata, Viewport } from "next"
import { Provider } from "@/components/provider"
import { plexMono, plexSans } from "@/lib/fonts"
import { appName, homeTitle, siteDescription, siteImage, siteUrl } from "@/lib/shared"
import "./global.css"

export const metadata: Metadata = {
  metadataBase: new URL(siteUrl),
  title: { default: homeTitle, template: `%s · ${appName}` },
  description: siteDescription,
  applicationName: appName,
  authors: [{ name: "Zaki", url: "https://zakiego.com" }],
  openGraph: { type: "website", siteName: appName, locale: "en_US", images: [siteImage] },
  twitter: { card: "summary_large_image", images: [siteImage] },
}

export const viewport: Viewport = {
  // The site opens dark whatever the OS says (landing always, docs by default).
  themeColor: "#0a0a0a",
}

export default function Layout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={`${plexSans.variable} ${plexMono.variable}`} suppressHydrationWarning>
      <body className="flex min-h-svh flex-col">
        <Provider>{children}</Provider>
      </body>
    </html>
  )
}
