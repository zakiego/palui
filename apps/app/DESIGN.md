# Palui dashboard: design

The contract is `docs/PLAN.md` section 11. This file records how the build meets it and the calls made along the way.

## Contract (from PLAN 11.1)

- Audience: one technical owner, often on a phone between other things.
- Primary task: see that Palui is alive, and flip a Saklar in seconds.
- Category default: a chatbot SaaS dashboard. Predictable opposite: a raw settings list.
- What's specific: health and control are the same element. Every connector shows on, paused or broken, and it's one tap from off.
- Required states per page: loading (Skeleton in the final layout), empty (Empty with the one next step), error (Alert with cause and fix), long content, 390 px.
- Finish counts: 1 accent hue, radius only from `--radius` steps, 0 emoji in chrome, 0 gradients, 0 inert controls.

## Foundations

- Sibling of recall: shadcn `base-nova`, `baseColor: neutral`, recall's oklch token file (`src/styles/app.css`), Geist and Geist Mono, `--radius: 0.625rem`, the `emphasized` and `drawer` easings.
- Shell: shadcn `sidebar-07` (collapses to icons, a Sheet on a phone), sticky header with the page title, the global status and ⌘K.
- Machine-written values (ids, model names, token counts, RRULEs, times) are Geist Mono with tabular numbers.
- Status is always a word plus an icon: on = green + check, paused = zinc + pause, error = red + alert, connecting = Spinner, not set up = dashed circle. See `src/components/connector-status.tsx`.
- Copy: English UI strings through `src/lib/i18n` (`en`, `id`). No em dashes anywhere.

## Motion (PLAN 11.6)

| Element | Decision |
|---|---|
| Saklar Switch | shadcn default thumb slide only |
| Status change | `.status-tone`: 150 ms color crossfade, no movement |
| Sheet / Drawer | shadcn defaults; Drawer drags to dismiss. `ResponsiveSheet` opens one commit after mount so a sheet that mounts open still slides in, and pages keep the last content while it closes so the exit plays |
| Home connector map | `.map-trunk` / `.map-branch`: the wires draw once ever behind a localStorage flag (40 ms linear trunk per row, chained, then a 120 ms branch), then static. Busy means a Spinner badge, no pulses |
| Rows chevron | 150 ms rotate on the emphasized curve, a CSS transition so it retargets |
| Login and invite | `.stagger-in`: 300 ms rise of 8 px, 60 ms apart, mount only (rare screens) |
| Agent reply | `.agent-in`: 150 ms opacity once when its text starts streaming. The owner's own messages never animate |
| Command menu (⌘K) | none: opened from the keyboard many times a day, so it appears at once |
| Tables, lists | none |
| Toasts | Sonner default |
| Reduced motion | everything instant (the global rule in `app.css`); spinners keep turning |

## Interaction (PLAN 11.5)

- Pause never asks: the Switch moves at once and a Sonner toast offers Undo. Remove always asks, in an AlertDialog that names the consequence (`manifest.removeWarning`).
- Test, Connect and Run now use `toast.promise`.
- Sheets become Drawers under 768 px (`src/components/responsive-sheet.tsx`).
- The connect wizard keeps progress as a `not_set_up` draft when the sheet closes.
- The header status is live over SSE and announced through `aria-live="polite"` once per change, never on load.

## Decisions

- 2026-11-10, accent (provisional): Banjar sasirangan indigo is the one accent hue, `oklch(0.45 0.15 270)` in light and `oklch(0.7 0.13 270)` in dark (label flips to ink at night). It drives `--primary` (buttons, the active Switch, my chat bubbles, the mark) and `--chart-1`. Chosen from PLAN 11.2's candidates because it can't be read as a status: green is on, zinc is paused, red is broken, and amber was already out. Provisional until the M3.5 Explore. The Explore (below) kept it: the Board variant's plugged cables use it too, and nothing in the three variants needed a second hue.
- 2026-11-10, Saklar layout (Claude's pick, Zaki to confirm): the PLAN 11.7 Explore ran on real data with three variants. 1 Cards, a card grid grouped by kind with a Switch per card (axis: closest to TASTE.md and shadcn). 2 Rows, dense Raycast or macOS settings rows with inline status and a chevron that opens the details in place (axis: density). 3 Board, a wiring board with Palui in the middle, channels plugged in on the left and everything Palui uses on the right, a solid cable when running and a dashed one when unplugged (axis: spatial). Kept: Cards. Why: it follows TASTE.md most closely (an icon on every node, big labelled cards instead of small controls) and is the layout PLAN 11.4 describes, the card shows the one-line summary, status, Switch and Open without an extra tap (Rows hides the details behind the chevron), and it holds up at 390 px with long MCP names, where Board doubles the page length and its left column floats beside a tall right one on desktop. Board is the most specific to the product and the best runner-up if the Home map ever needs to carry more. To compare: open `/app/saklar?v=1` (Cards), `?v=2` (Rows) or `?v=3` (Board); the picker sits at the bottom, and 1, 2, 3 or the arrow keys switch. Without `?v` the page shows the pick (`DEFAULT_SAKLAR_LAYOUT` in `src/components/saklar/layouts.ts`). On confirm, delete the other two variants, the picker and the `v` search param.
- 2026-11-10, Home Saklar summary: a connector map that follows the Cards pick. Palui sits at the root and every connector hangs off one wire, grouped by kind, each with an icon tile, its status word and icon, and its Switch. The wires draw once ever (the `palui.homeMap.drawn` localStorage flag): each row's trunk segment takes 40 ms, linear, chained so the wire reads as one line, then each branch reaches out in 120 ms on the emphasized curve. Then it's static. A connecting connector shows the Spinner badge, never a pulse. Reduced motion: no draw. Known limit: on a first ever visit that's a full page load (not a client navigation), the static wires paint before hydration and then draw once; the usual first visit comes from the invite flow, which is a client navigation.
- 2026-11-10, chat Markdown: agent replies render a small safe subset (bold, italic, inline code, fenced code, bullet and numbered lists, links and bare URLs) through a hand-written parser in `src/lib/markdown.ts` that returns a tree, rendered as React elements in `src/components/markdown.tsx`. No raw HTML, no `dangerouslySetInnerHTML`, links only for http, https and mailto with `rel="noopener noreferrer"`. Chosen over react-markdown because the subset is small, needs no plugin chain, and the parser is unit-tested in `markdown.test.ts`. The owner's own messages stay plain text.
- 2026-11-10, Drawer: shadcn's base-nova `drawer` (Base UI's Drawer) instead of a hand-wired vaul, because the shadcn component is the house rule. It has the same drag-to-dismiss behavior.
- 2026-11-10, toasts: Sonner (`components/ui/sonner.tsx`, reading this app's theme provider) instead of base-nova's Base UI toast, because PLAN 3.2 names Sonner.
- 2026-11-10, chat: shadcn's chat primitives (`message-scroller`, `message`, `bubble`, `trace`) for the transcript and inline tool calls, instead of AI Elements.
