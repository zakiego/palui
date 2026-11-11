// Dev only: the realistic upper bound for list length. A year of daily use
// leaves about this much behind: a thousand chats and reminders, thousands of
// runs and events. None of the lists paginate, so this is a render and query
// cost test as much as a visual one.
import { D, H, MIN, type Kit } from "./kit"

const FIRST = ["Aleksandra", "Budi", "Christopher", "Dewi", "Eka", "Fitri", "Gusti", "Hendra", "Indah", "Joko", "Kartika", "Lukman", "Maria", "Nur", "Oktaviani", "Putu", "Rahmat", "Siti", "Taufik", "Wulan"]
const LAST = ["Wiśniewska-Kowalczyk", "Santoso", "Montgomery", "Lestari", "Pratama", "Handayani", "Ngurah", "Gunawan", "Permatasari", "Widodo", "Sari", "Hakim", "Fernández", "Hidayah", "Putri", "Wijaya", "Hidayat", "Aminah", "Rahman", "Ningsih"]
const TASKS = ["Follow up deal", "Telepon pak RT soal iuran keamanan", "Bayar listrik", "Kirim laporan mingguan ke tim Vorneo", "Standup", "Cek pipeline", "Perpanjang domain", "Transfer arisan"]

export const HUGE = { chats: 1284, transcript: 2000, upcoming: 1284, recurring: 300, done: 1284, jobs: 120, runs: 5000, events: 12_000, mcp: 50, hooks: 40, passkeys: 25 }

export function huge(k: Kit) {
  const { now } = k
  const name = (i: number) => `${FIRST[i % FIRST.length]} ${LAST[Math.floor(i / FIRST.length) % LAST.length]}`

  k.connector("telegram", "telegram", { status: "on" })
  k.connector("whatsapp", "whatsapp", { status: "on" })
  k.connector("web", "web", { status: "on" })
  k.connector("cliproxy", "cliproxy", { status: "on" })
  k.connector("ai-gateway", "ai-gateway", { status: "on" })
  k.connector("recall", "recall", { status: "on" })
  k.connector("reminders", "reminders", { status: "on" })
  k.connector("jobs", "jobs", { status: "on" })
  k.connector("webfetch", "webfetch", { status: "on" })
  k.connector("hooks", "hooks", { status: "on" })
  // Every MCP server goes down together when the network does.
  for (let i = 0; i < HUGE.mcp; i++) {
    k.connector(`mcp-server-${String(i).padStart(2, "0")}`, "mcp", { label: `${LAST[i % LAST.length]} MCP ${i + 1}`, status: i % 4 === 0 ? "error" : i % 7 === 0 ? "paused" : "on", detail: i % 4 === 0 ? "ECONNRESET" : undefined, trust: i % 3 ? "external" : "own", config: { url: `https://mcp-${i}.example.com/mcp` } })
  }

  const web = k.webChat()
  for (let i = 0; i < HUGE.transcript; i++) {
    k.message(web, i % 2 ? "assistant" : "user", i % 2 ? `Siap, sudah dicatat (${i}).` : `${TASKS[i % TASKS.length]} #${1000 + i}`, now - (HUGE.transcript - i) * MIN)
  }

  const chatIds: string[] = []
  for (let i = 0; i < HUGE.chats; i++) {
    const channel = i % 2 ? "telegram" : "whatsapp"
    const c = k.chat(channel, channel === "telegram" ? String(1_000_000_000 + i) : `62812${String(10_000_000 + i)}@s.whatsapp.net`, name(i), { kind: i % 9 === 0 ? "group" : "dm" })
    chatIds.push(c.id)
    k.message(c, i % 3 ? "user" : "assistant", `${TASKS[i % TASKS.length]}, ${name(i + 7)}`, now - i * 37 * MIN)
  }
  const chatAt = (i: number) => chatIds[i % chatIds.length]!

  const jobIds: string[] = []
  for (let i = 0; i < HUGE.jobs; i++) {
    jobIds.push(k.job({ name: `${TASKS[i % TASKS.length]} ${i + 1}`, kind: i % 10 === 0 ? "briefing" : i % 10 === 1 ? "heartbeat" : "job", rrule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", tz: "Asia/Makassar", prompt: "x", chatId: chatAt(i), enabled: i % 6 !== 0, nextRunAt: now + (i + 1) * 10 * MIN, lastRunAt: now - (i + 1) * 10 * MIN, lastStatus: i % 8 === 0 ? "error" : "ok", lastResult: "Selesai.", dailyTokenCap: 50_000, createdAt: now - D + i }))
  }

  for (let i = 0; i < HUGE.runs; i++) {
    k.run({ chatId: chatAt(i), jobId: i % 5 === 0 ? jobIds[i % jobIds.length]! : null, trigger: i % 5 === 0 ? "schedule" : "message", model: `model-${String(i % 26).padStart(2, "0")}`, tokensIn: 4000 + (i % 90) * 150, tokensOut: 300 + (i % 40) * 10, status: i % 31 === 0 ? "error" : "ok", error: i % 31 === 0 ? "429 from CLIProxyAPI" : null, startedAt: now - i * 9 * MIN, ms: 2000 + (i % 50) * 300 })
  }

  for (let i = 0; i < HUGE.upcoming; i++) k.reminder({ chatId: chatAt(i), text: `${TASKS[i % TASKS.length]} #${1000 + i}`, tz: "Asia/Makassar", fireAt: now + (i + 1) * 23 * MIN, status: "pending" })
  for (let i = 0; i < HUGE.recurring; i++) k.reminder({ chatId: chatAt(i), text: `${TASKS[i % TASKS.length]} (rutin ${i + 1})`, rrule: i % 2 ? "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" : "FREQ=MONTHLY;BYMONTHDAY=1", tz: "Asia/Makassar", fireAt: now + (i + 1) * 47 * MIN, status: "pending" })
  for (let i = 0; i < HUGE.done; i++) k.reminder({ chatId: chatAt(i), text: `${TASKS[i % TASKS.length]} #${i}`, tz: "Asia/Makassar", fireAt: now - (i + 1) * H, status: i % 4 ? "done" : "cancelled", updatedAt: now - (i + 1) * H })

  for (let i = HUGE.events; i > 0; i--) {
    k.event({ at: now - i * 3 * MIN, level: i % 53 === 0 ? "error" : i % 17 === 0 ? "warn" : "info", connectorId: i % 3 ? `mcp-server-${String(i % HUGE.mcp).padStart(2, "0")}` : "cliproxy", kind: i % 4 ? "tool" : "status", message: i % 4 ? `list_deals ok in ${100 + (i % 70) * 13} ms` : "status change" })
  }

  for (let i = 0; i < HUGE.hooks; i++) k.hook({ name: `${LAST[i % LAST.length]} alerts ${i + 1}`, chatId: chatAt(i), mode: i % 2 ? "summarize" : "forward", lastAt: i % 5 ? now - i * H : null })
  for (let i = 0; i < HUGE.passkeys; i++) k.passkey({ name: `${name(i)}'s device ${i + 1}`, deviceType: i % 2 ? "singleDevice" : "multiDevice", createdAt: now - i * D })
}
