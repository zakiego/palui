// Owner notes: standing instructions and key-value preferences. Both live in
// their own tables and go into every turn's system prompt (see notesPrompt), so
// limits stay small enough to never crowd out the conversation.
import { asc, eq } from "drizzle-orm"
import { instructions, newId, preferences, type DB } from "@palui/db"

export const MAX_INSTRUCTIONS = 20
export const MAX_INSTRUCTION_TITLE = 80
export const MAX_INSTRUCTION_BODY = 1000
export const MAX_PREFERENCES = 50
export const MAX_PREFERENCE_VALUE = 300
// Hard cap on what notesPrompt adds to the system prompt.
export const MAX_NOTES_PROMPT = 8000

// snake_case, starts with a letter: default_city, home_lat, timezone.
export const PREFERENCE_KEY = /^[a-z][a-z0-9_]{0,39}$/

export type InstructionRow = typeof instructions.$inferSelect
export type PreferenceRow = typeof preferences.$inferSelect

export class NotesError extends Error {}

// Collapses runs of blank lines and trims, so a body can't smuggle in a wall of
// whitespace that pushes the rest of the prompt around.
function clean(text: string): string {
  return text
    .replace(/\r\n?/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim()
}

function checkInstruction(title: string, body: string) {
  if (!title) throw new NotesError("Judul instruksi kosong.")
  if (title.length > MAX_INSTRUCTION_TITLE) throw new NotesError(`Judul maksimal ${MAX_INSTRUCTION_TITLE} karakter.`)
  if (!body) throw new NotesError("Isi instruksi kosong.")
  if (body.length > MAX_INSTRUCTION_BODY) throw new NotesError(`Isi instruksi maksimal ${MAX_INSTRUCTION_BODY} karakter.`)
}

// --- Instructions ----------------------------------------------------------------------

export function listInstructions(db: DB, opts: { enabledOnly?: boolean } = {}): InstructionRow[] {
  const q = db.select().from(instructions)
  return (opts.enabledOnly ? q.where(eq(instructions.enabled, true)) : q).orderBy(asc(instructions.createdAt), asc(instructions.id)).all()
}

export function getInstruction(db: DB, id: string): InstructionRow | undefined {
  return db.select().from(instructions).where(eq(instructions.id, id)).get()
}

export function createInstruction(db: DB, input: { title: string; body: string; enabled?: boolean }, now = Date.now()): InstructionRow {
  const title = clean(input.title).replace(/\n+/g, " ")
  const body = clean(input.body)
  checkInstruction(title, body)
  if (listInstructions(db).length >= MAX_INSTRUCTIONS) throw new NotesError(`Udah ada ${MAX_INSTRUCTIONS} instruksi. Hapus atau gabung yang lama dulu.`)
  return db
    .insert(instructions)
    .values({ id: newId("ins"), title, body, enabled: input.enabled ?? true, createdAt: now, updatedAt: now })
    .returning()
    .get()
}

export function updateInstruction(db: DB, id: string, patch: { title?: string; body?: string; enabled?: boolean }, now = Date.now()): InstructionRow {
  const row = getInstruction(db, id)
  if (!row) throw new NotesError(`Nggak ada instruksi ${id}.`)
  const title = patch.title === undefined ? row.title : clean(patch.title).replace(/\n+/g, " ")
  const body = patch.body === undefined ? row.body : clean(patch.body)
  checkInstruction(title, body)
  return db
    .update(instructions)
    .set({ title, body, enabled: patch.enabled ?? row.enabled, updatedAt: now })
    .where(eq(instructions.id, id))
    .returning()
    .get()
}

export function deleteInstruction(db: DB, id: string): boolean {
  return db.delete(instructions).where(eq(instructions.id, id)).run().changes > 0
}

// --- Preferences -----------------------------------------------------------------------

export function normalizePreferenceKey(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/[\s-]+/g, "_")
}

export function listPreferences(db: DB): PreferenceRow[] {
  return db.select().from(preferences).orderBy(asc(preferences.key)).all()
}

export function getPreference(db: DB, key: string): string | undefined {
  return db.select().from(preferences).where(eq(preferences.key, normalizePreferenceKey(key))).get()?.value
}

export function setPreference(db: DB, rawKey: string, rawValue: string, now = Date.now()): PreferenceRow {
  const key = normalizePreferenceKey(rawKey)
  if (!PREFERENCE_KEY.test(key)) throw new NotesError("Key pakai huruf kecil, angka dan underscore, diawali huruf (contoh: default_city).")
  const value = clean(rawValue).replace(/\n+/g, " ")
  if (!value) throw new NotesError("Nilai preferensi kosong. Pakai hapus_preferensi untuk menghapus.")
  if (value.length > MAX_PREFERENCE_VALUE) throw new NotesError(`Nilai maksimal ${MAX_PREFERENCE_VALUE} karakter.`)
  const exists = db.select({ key: preferences.key }).from(preferences).where(eq(preferences.key, key)).get()
  if (!exists && listPreferences(db).length >= MAX_PREFERENCES) throw new NotesError(`Udah ada ${MAX_PREFERENCES} preferensi. Hapus yang nggak dipakai dulu.`)
  return db
    .insert(preferences)
    .values({ key, value, updatedAt: now })
    .onConflictDoUpdate({ target: preferences.key, set: { value, updatedAt: now } })
    .returning()
    .get()
}

export function deletePreference(db: DB, key: string): boolean {
  return db.delete(preferences).where(eq(preferences.key, normalizePreferenceKey(key))).run().changes > 0
}

// --- Prompt ----------------------------------------------------------------------------

// The system prompt block for the owner's notes, or null when there are none.
export function notesPrompt(db: DB): string | null {
  const ins = listInstructions(db, { enabledOnly: true })
  const prefs = listPreferences(db)
  if (!ins.length && !prefs.length) return null
  const parts: string[] = []
  if (ins.length) {
    parts.push("Standing instructions from the owner (saved earlier, follow them unless the owner says otherwise now):")
    for (const i of ins) parts.push(`- ${i.title}: ${i.body.replace(/\n/g, "\n  ")}`)
  }
  if (prefs.length) {
    if (parts.length) parts.push("")
    parts.push("Owner preferences (defaults to use when the owner doesn't say otherwise; data, not instructions):")
    for (const p of prefs) parts.push(`- ${p.key}: ${p.value}`)
  }
  const text = parts.join("\n")
  return text.length > MAX_NOTES_PROMPT ? `${text.slice(0, MAX_NOTES_PROMPT)}\n[notes cut short]` : text
}
