import { IBM_Plex_Mono, IBM_Plex_Sans } from "next/font/google"

// One superfamily for the whole site, landing and docs. Plex Mono's slab
// serifs on i, l and 1 keep terminal lines readable, and its true italic sets
// a quoted chat line apart without a second color. Plex Sans covers prose.
export const plexSans = IBM_Plex_Sans({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-plex-sans",
  display: "swap",
})

export const plexMono = IBM_Plex_Mono({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  style: ["normal", "italic"],
  variable: "--font-plex-mono",
  display: "swap",
})
