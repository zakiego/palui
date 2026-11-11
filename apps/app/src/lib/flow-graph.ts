// Lays a flow definition out as a top-down graph: triggers on top, then each
// step one rank below the furthest step that leads into it, and a small "end"
// under every way a run can finish. Flows have at most 40 steps and can't loop
// (validate.ts rejects cycles), so a hand-written layered layout is enough and
// no graph library is needed. Pure, so it's tested without a browser.

export type GraphStep = { id: string; edges: { label: string; to: string | null }[]; ends: boolean }
export type GraphTrigger = { id: string; start: string }

export type NodeKind = "trigger" | "step" | "end"
export type EdgeTone = "next" | "branch" | "error" | "body"

export type LaidNode = { key: string; kind: NodeKind; ref: string; x: number; y: number; w: number; h: number; rank: number }
export type LaidEdge = { key: string; from: string; to: string; label: string; tone: EdgeTone; path: string; labelAt: { x: number; y: number } | null }
export type FlowLayout = { nodes: LaidNode[]; edges: LaidEdge[]; width: number; height: number }

export const NODE_SIZE: Record<NodeKind, { w: number; h: number }> = {
  trigger: { w: 200, h: 32 },
  step: { w: 216, h: 76 },
  end: { w: 56, h: 24 },
}

const GAP_X = 28
const GAP_Y = 48
const PAD = 16
const LANE = 16

export const stepKey = (id: string) => `s:${id}`
// Triggers that start at the same step share one node, keyed by that step.
export const triggerKey = (start: string) => `t:${start}`

type RawEdge = { from: string; to: string; label: string }

function toneOf(label: string): EdgeTone {
  if (label === "onError") return "error"
  if (label === "body") return "body"
  if (label === "next" || label === "") return "next"
  return "branch"
}

export function layoutFlow(steps: GraphStep[], triggers: GraphTrigger[]): FlowLayout {
  const known = new Set(steps.map((s) => s.id))
  const kinds = new Map<string, NodeKind>()
  const refs = new Map<string, string>()
  const raw: RawEdge[] = []
  const add = (key: string, kind: NodeKind, ref: string) => {
    kinds.set(key, kind)
    refs.set(key, ref)
  }

  const liveTriggers = [...new Set(triggers.filter((t) => known.has(t.start)).map((t) => t.start))]
  for (const start of liveTriggers) {
    add(triggerKey(start), "trigger", start)
    raw.push({ from: triggerKey(start), to: stepKey(start), label: "" })
  }
  for (const s of steps) add(stepKey(s.id), "step", s.id)
  for (const s of steps) {
    let ends = 0
    const end = (label: string) => {
      const key = `e:${s.id}:${ends++}`
      add(key, "end", s.id)
      raw.push({ from: stepKey(s.id), to: key, label })
    }
    for (const e of s.edges) {
      if (e.to === null) end(e.label)
      else if (known.has(e.to)) raw.push({ from: stepKey(s.id), to: stepKey(e.to), label: e.label })
    }
    if (s.ends && !s.edges.some((e) => e.to === null)) end("")
  }

  const keys = [...kinds.keys()]
  const out = new Map<string, RawEdge[]>(keys.map((k) => [k, []]))
  for (const e of raw) out.get(e.from)!.push(e)

  // Depth-first from the triggers (or the first step) gives a reading order and
  // finds any edge that points back up, which only an old, invalid version has.
  const roots = liveTriggers.length ? liveTriggers.map(triggerKey) : steps.length ? [stepKey(steps[0]!.id)] : []
  const seen = new Map<string, 1 | 2>()
  const order: string[] = []
  const back = new Set<RawEdge>()
  const visit = (k: string) => {
    seen.set(k, 1)
    order.push(k)
    for (const e of out.get(k)!) {
      const st = seen.get(e.to)
      if (st === 1) back.add(e)
      else if (!st) visit(e.to)
    }
    seen.set(k, 2)
  }
  for (const r of roots) if (!seen.has(r)) visit(r)
  for (const k of keys) if (!seen.has(k) && kinds.get(k) !== "end") visit(k)

  // Rank by longest path over the forward edges, so a step sits below every
  // step that can lead into it.
  const fwd = raw.filter((e) => !back.has(e))
  const indeg = new Map(keys.map((k) => [k, 0]))
  for (const e of fwd) indeg.set(e.to, indeg.get(e.to)! + 1)
  const rank = new Map<string, number>()
  const floor = liveTriggers.length ? 1 : 0
  for (const k of keys) if (indeg.get(k) === 0) rank.set(k, kinds.get(k) === "trigger" ? 0 : floor)
  const queue = keys.filter((k) => indeg.get(k) === 0)
  while (queue.length) {
    const k = queue.shift()!
    for (const e of fwd.filter((x) => x.from === k)) {
      rank.set(e.to, Math.max(rank.get(e.to) ?? 0, rank.get(k)! + 1))
      indeg.set(e.to, indeg.get(e.to)! - 1)
      if (indeg.get(e.to) === 0) queue.push(e.to)
    }
  }

  const ranks: string[][] = []
  for (const k of order) (ranks[rank.get(k)!] ??= []).push(k)
  for (let r = 0; r < ranks.length; r++) ranks[r] ??= []

  // A few barycenter sweeps to cut crossings: each node moves toward the
  // average position of its parents (going down) or children (going up).
  const parents = new Map<string, string[]>(keys.map((k) => [k, []]))
  const children = new Map<string, string[]>(keys.map((k) => [k, []]))
  for (const e of fwd) {
    parents.get(e.to)!.push(e.from)
    children.get(e.from)!.push(e.to)
  }
  const pos = new Map<string, number>()
  const index = () => ranks.forEach((row) => row.forEach((k, i) => pos.set(k, i)))
  index()
  const sweep = (row: string[], near: Map<string, string[]>) => {
    const bary = new Map(row.map((k, i) => {
      const ns = near.get(k)!
      return [k, ns.length ? ns.reduce((a, n) => a + pos.get(n)!, 0) / ns.length : i] as const
    }))
    row.sort((a, b) => bary.get(a)! - bary.get(b)!)
    row.forEach((k, i) => pos.set(k, i))
  }
  for (let pass = 0; pass < 4; pass++) {
    for (let r = 1; r < ranks.length; r++) sweep(ranks[r]!, parents)
    for (let r = ranks.length - 2; r >= 0; r--) sweep(ranks[r]!, children)
  }
  for (let r = 1; r < ranks.length; r++) sweep(ranks[r]!, parents)
  index()

  // x: each node wants to sit under the middle of its parents. Packing the row
  // from the left and from the right and averaging the two keeps the spacing
  // and centers the row on what it wants.
  const size = (k: string) => NODE_SIZE[kinds.get(k)!]
  const cx = new Map<string, number>()
  for (const row of ranks) {
    const want = row.map((k) => {
      const ps = parents.get(k)!.filter((p) => cx.has(p))
      return ps.length ? ps.reduce((a, p) => a + cx.get(p)!, 0) / ps.length : null
    })
    const sep = (i: number) => size(row[i]!).w / 2 + GAP_X + size(row[i + 1]!).w / 2
    // Fill the gaps: a node without parents takes a slot next to its neighbor.
    const first = want.findIndex((w) => w !== null)
    if (first < 0) {
      let x = 0
      row.forEach((_, i) => {
        want[i] = x
        if (i < row.length - 1) x += sep(i)
      })
    } else {
      for (let i = first - 1; i >= 0; i--) want[i] = want[i + 1]! - sep(i)
      for (let i = first + 1; i < row.length; i++) if (want[i] === null) want[i] = want[i - 1]! + sep(i - 1)
    }
    const left = want.map((w) => w!)
    for (let i = 1; i < row.length; i++) left[i] = Math.max(left[i]!, left[i - 1]! + sep(i - 1))
    const right = want.map((w) => w!)
    for (let i = row.length - 2; i >= 0; i--) right[i] = Math.min(right[i]!, right[i + 1]! - sep(i))
    row.forEach((k, i) => cx.set(k, (left[i]! + right[i]!) / 2))
  }

  // y: one row per rank, as tall as its tallest node.
  const rowH = ranks.map((row) => Math.max(0, ...row.map((k) => size(k).h)))
  const rowTop: number[] = []
  let y = PAD
  for (let r = 0; r < ranks.length; r++) {
    rowTop.push(y)
    y += rowH[r]! + GAP_Y
  }
  const height = y - GAP_Y + PAD

  const minX = Math.min(...keys.map((k) => cx.get(k)! - size(k).w / 2))
  const nodes: LaidNode[] = keys.map((k) => {
    const { w, h } = size(k)
    const r = rank.get(k)!
    return { key: k, kind: kinds.get(k)!, ref: refs.get(k)!, x: cx.get(k)! - w / 2 - minX + PAD, y: rowTop[r]! + (rowH[r]! - h) / 2, w, h, rank: r }
  })
  const byKey = new Map(nodes.map((n) => [n.key, n]))
  let width = Math.max(0, ...nodes.map((n) => n.x + n.w)) + PAD

  // Ports: several edges leaving (or entering) one node spread along its edge,
  // ordered by where the other end sits, so they don't start on one point.
  const spread = (n: LaidNode, list: RawEdge[], other: (e: RawEdge) => LaidNode) => {
    const sorted = [...list].sort((a, b) => other(a).x - other(b).x)
    const step = Math.min(24, n.w / (sorted.length + 1))
    return new Map(sorted.map((e, i) => [e, n.x + n.w / 2 + (i - (sorted.length - 1) / 2) * step]))
  }
  const outPort = new Map<RawEdge, number>()
  const inPort = new Map<RawEdge, number>()
  for (const n of nodes) {
    for (const [e, x] of spread(n, fwd.filter((e) => e.from === n.key), (e) => byKey.get(e.to)!)) outPort.set(e, x)
    for (const [e, x] of spread(n, fwd.filter((e) => e.to === n.key), (e) => byKey.get(e.from)!)) inPort.set(e, x)
  }

  const edges: LaidEdge[] = []
  let lanes = 0
  for (const e of raw) {
    const a = byKey.get(e.from)!
    const b = byKey.get(e.to)!
    const tone = toneOf(e.label)
    const label = tone === "next" ? "" : e.label
    const key = `${e.from}>${e.to}:${e.label}`
    if (back.has(e)) {
      // Up the right side, past everything in between.
      const lane = width + LANE * lanes++
      const sy = a.y + a.h / 2
      const ty = b.y + b.h / 2
      const path = `M ${a.x + a.w} ${sy} H ${lane - 8} Q ${lane} ${sy} ${lane} ${sy - 8} V ${ty + 8} Q ${lane} ${ty} ${lane - 8} ${ty} H ${b.x + b.w}`
      edges.push({ key, from: e.from, to: e.to, label, tone, path, labelAt: label ? { x: lane, y: (sy + ty) / 2 } : null })
      continue
    }
    const sx = outPort.get(e)!
    const sy = a.y + a.h
    const tx = inPort.get(e)!
    const ty = b.y
    // A straight drop is fine unless a node in a row in between is in the way;
    // then the edge goes down a lane beside those nodes.
    const between = nodes.filter((n) => n.rank > a.rank && n.rank < b.rank)
    const blocks = (x: number, n: LaidNode) => x > n.x - 6 && x < n.x + n.w + 6
    const blocked = between.filter((n) => {
      const t = (n.y + n.h / 2 - sy) / (ty - sy)
      return blocks(sx + (tx - sx) * t, n)
    })
    if (!blocked.length) {
      const dy = (ty - sy) / 2
      const path = `M ${sx} ${sy} C ${sx} ${sy + dy} ${tx} ${ty - dy} ${tx} ${ty}`
      edges.push({ key, from: e.from, to: e.to, label, tone, path, labelAt: label ? cubicAt(sx, sy, tx, ty, 0.3) : null })
      continue
    }
    const rightLane = Math.max(...between.map((n) => n.x + n.w)) + LANE
    const leftLane = Math.min(...between.map((n) => n.x)) - LANE
    const mid = (sx + tx) / 2
    const lane = Math.abs(rightLane - mid) <= Math.abs(mid - leftLane) || leftLane < PAD ? rightLane : leftLane
    width = Math.max(width, lane + PAD)
    const y1 = rowTop[a.rank]! + rowH[a.rank]! + GAP_Y / 2
    const y2 = rowTop[b.rank]! - GAP_Y / 2
    const d1 = (y1 - sy) / 2
    const d2 = (ty - y2) / 2
    const path = `M ${sx} ${sy} C ${sx} ${sy + d1} ${lane} ${y1 - d1} ${lane} ${y1} V ${y2} C ${lane} ${y2 + d2} ${tx} ${ty - d2} ${tx} ${ty}`
    edges.push({ key, from: e.from, to: e.to, label, tone, path, labelAt: label ? { x: lane, y: (y1 + y2) / 2 } : null })
  }

  return { nodes, edges, width, height }
}

// A point on the vertical S-curve used for single-row edges, for its label.
function cubicAt(sx: number, sy: number, tx: number, ty: number, t: number) {
  const dy = (ty - sy) / 2
  const u = 1 - t
  const x = u * u * u * sx + 3 * u * u * t * sx + 3 * u * t * t * tx + t * t * t * tx
  const y = u * u * u * sy + 3 * u * u * t * (sy + dy) + 3 * u * t * t * (ty - dy) + t * t * t * ty
  return { x, y }
}
