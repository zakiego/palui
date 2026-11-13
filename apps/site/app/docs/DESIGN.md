# Palui docs design

The docs under /docs. `apps/site/DESIGN.md` covers the landing and the brand page; this file covers the docs chrome and how MDX renders. It lives next to `docs.css` so a docs change and a landing change never edit the same file.

## Contract

- Audience: engineers reading how Palui works, often from a link in the dashboard or a chat.
- Primary task: read one page, then find the next one (sidebar, contents, search).
- Category default: stock Fumadocs, which is what the docs were.
- What's specific: the docs read like the Palui dashboard (`apps/app/DESIGN.md`, Linear redesign), with the same faces, neutrals and radius steps, so the product feels like one thing.
- Content rule: the pages' words are not this file's business. Only the chrome and how content renders: headings, code, callouts, tables, sidebar, contents, search.
- Required states: a long page with tables and code, the changelog, search open, 390 px, light and dark.

## Foundations

- Theme: `app/docs/docs.css`, imported only by the docs layout and scoped to `:root:has(#nd-docs-layout)`, so the landing (`.lp-root`) and the shared tokens in `app/global.css` stay as they are. `:root` rather than a wrapper class because the search dialog renders outside the layout.
- Faces: Inter (variable, opsz) at 510 and 590 with `cv01` and `ss03`, Geist Mono for code. The docs reuse the landing's next/font instances (`lib/landing-fonts.ts`) through `lib/docs-fonts.ts`, set on `:root` only on docs routes. The root layout keeps IBM Plex for everything else.
- Palette: the dashboard's. Night: `#08090a` page and sidebar, `#0f1011` cards, `#141516` popovers, `#1c1c1f` code and callouts, `#23252a` lines, text `#f7f8f8`, `#d0d6e0`, `#8a8f98`, faint `#62666d`. Day: `#fcfcfd` page, `#ffffff` cards, `#f2f2f4` fill, `#e6e6e9` lines, text `#1f2023`, `#3c4149`, `#6f6e77`. Indigo `#5e6ad2` only for the focus ring, the caret and selection. The docs open dark and can switch to light.
- Radius: 4 (inline code), 6 (sidebar rows), 8 (code, callouts, search rows), 12 (cards, the search dialog), pills for the search trigger, page actions and theme switch.
- Sidebar: Linear's, 14 px at 510, muted text, hover lifts the text only, the current page is full-strength text with no plate and no bar.
- Prose: 15/1.65 in the secondary text tier, headings 590 in ink with 56 px above an h2, links in ink with a faint underline.
- Code: the fill step, a 5% hairline, radius 8, Geist Mono 13 px, the copy button only on hover for a mouse.
- Tables: no card and no vertical rules, a 590 head over a hairline, quieter row rules.
- Callouts: shadcn `Alert` (`components/docs/callout.tsx`, `role="note"`). A flat 16 px icon, hued only for warning, error and success; info and idea stay neutral. No colored bar or fill.
- Changelog: its contents list only the releases (h2), not every Added, Changed and Fixed.

## Motion

- Hover: color only, 150 ms ease-out. Sidebar rows light up at once and fade back over 150 ms.
- Press: cards and page actions scale to 0.98 and 0.97, 150 ms.
- Code copy button: fades in over 200 ms on hover or focus, fine pointers only; always shown on touch.
- Search: none. Opened from the keyboard many times a visit, so it appears and leaves at once, like the dashboard's ⌘K menu (Fumadocs scales it in from 1.06 over 300 ms).
- Everything else Fumadocs does (the drawer slide, folder open) stays. Reduced motion is handled by `app/global.css`.

## Decisions

- 2026-11-13, docs in the dashboard's Linear look (the agent's pick, the owner wasn't watching the run). The owner named Linear's docs and the dashboard redesign (PR #93) as the reference, which counted as a clear brief, so no variants were built behind a picker. Three directions were weighed. 1 Linear docs on Fumadocs' own layout, restyled through tokens and scoped CSS (axis: closest to the taste log, no fight with Fumadocs). 2 Dark only, like the landing, dropping the light theme (axis: one mood). 3 Fumadocs' Notebook layout with a top bar and breadcrumbs, closer to Linear's docs shell (axis: structure). Kept 1. Why: it keeps the light theme readers already use, keeps every route and Fumadocs behavior, and touched no shared token or font file, so it merged cleanly beside the landing redesign (#95). 2 would remove a working theme. 3 is the runner-up if the docs ever get a top-level nav, but it moves the search, sidebar and theme switch, and on a phone it adds a second bar.
