import { geistMono, inter } from "./landing-fonts"

// The docs read in the landing's and the dashboard's faces, from the same
// next/font instances (lib/landing-fonts.ts), so the site ships one Inter
// and one Geist Mono. app/docs/docs.css reads them through --docs-sans and
// --docs-mono, set on :root by the docs layout so the search dialog, which
// renders outside the layout, gets them too.
export const docsFontVars = `:root{--docs-sans:${inter.style.fontFamily};--docs-mono:${geistMono.style.fontFamily}}`
