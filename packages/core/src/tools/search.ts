// Tool search (PLAN 5.2): when a turn has more tools than budgets.maxTools, only a
// core set is loaded and the rest is found by keyword through `palui__cari_tool`.
// Pure ranking here; loading lives in collect.ts.

export type SearchDoc = {
  // Namespaced tool name, `<connectorId>__<tool>`.
  name: string
  connectorId: string
  connectorLabel: string
  tool: string
  title?: string
  description: string
}

export type SearchHit = { doc: SearchDoc; score: number }

// Lowercase words, split on anything that isn't a letter or digit, so
// `get_prayer_times`, `jadwal-sholat` and "Jadwal Sholat" all tokenize alike.
export function tokens(s: string): string[] {
  return s
    .toLowerCase()
    .split(/[^\p{L}\p{N}]+/u)
    .filter((t) => t.length > 1)
}

// Field weights: a word in the tool or connector name says more than one in a
// long description.
const FIELDS: { weight: number; text: (d: SearchDoc) => string }[] = [
  { weight: 4, text: (d) => d.tool },
  { weight: 3, text: (d) => `${d.connectorLabel} ${d.connectorId}` },
  { weight: 3, text: (d) => d.title ?? "" },
  { weight: 1, text: (d) => d.description },
]

function scoreDoc(doc: SearchDoc, query: string[]): number {
  let score = 0
  for (const f of FIELDS) {
    const words = tokens(f.text(doc))
    if (!words.length) continue
    for (const q of query) {
      if (words.includes(q)) score += f.weight * 2
      // Prefix either way: "sholat" finds "sholatnya", "prayer" finds "prayers".
      else if (q.length >= 3 && words.some((w) => w.length >= 3 && (w.startsWith(q) || q.startsWith(w)))) score += f.weight
    }
  }
  return score
}

// Best matches first; ties keep the input order (core connectors first).
export function searchTools(docs: SearchDoc[], query: string, limit: number): SearchHit[] {
  const q = [...new Set(tokens(query))]
  if (!q.length) return []
  return docs
    .map((doc, i) => ({ doc, score: scoreDoc(doc, q), i }))
    .filter((h) => h.score > 0)
    .sort((a, b) => b.score - a.score || a.i - b.i)
    .slice(0, limit)
    .map(({ doc, score }) => ({ doc, score }))
}
