import { Geist_Mono, Inter } from "next/font/google"

// The docs read in the dashboard's faces: Inter for prose (variable weight,
// so Linear's 510 and 590 land exactly, with optical sizing) and Geist Mono
// for code. Only the docs load them; app/docs/docs.css reads them through
// --docs-sans and --docs-mono, set on :root by the docs layout so the search
// dialog, which renders outside the layout, gets them too.
export const docsSans = Inter({
  subsets: ["latin"],
  axes: ["opsz"],
  display: "swap",
})

export const docsMono = Geist_Mono({
  subsets: ["latin"],
  display: "swap",
})

export const docsFontVars = `:root{--docs-sans:${docsSans.style.fontFamily};--docs-mono:${docsMono.style.fontFamily}}`
