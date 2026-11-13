import { Geist_Mono, Inter } from "next/font/google"

// The landing and the brand page use the dashboard's type: Inter (variable,
// so the 510 and 590 weights exist) and Geist Mono for values a machine
// wrote. The docs read the same instances through lib/docs-fonts.ts, so
// the root layout, and with it everything else, keeps IBM Plex.
export const inter = Inter({
  subsets: ["latin"],
  axes: ["opsz"],
  variable: "--font-inter",
  display: "swap",
})

export const geistMono = Geist_Mono({
  subsets: ["latin"],
  variable: "--font-geist-mono",
  display: "swap",
})
