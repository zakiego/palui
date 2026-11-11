// Chats page: the list, a transcript, the web chat input and approvals.
import { ensureWebChat } from "@palui/core/chats"
import { getSetting, setSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { chatList, transcript } from "@/server/queries"
import { callRuntime } from "@/server/runtime"
import { requireUser, requireWriter } from "@/server/session"

export const getChats = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const web = ensureWebChat(db)
  return { chats: chatList(db), webChatId: web.id }
})

export const getTranscript = createServerFn({ method: "GET" })
  .validator(z.object({ chatId: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    const t = transcript(db, data.chatId)
    if (!t) throw new Error("That chat doesn't exist anymore.")
    return t
  })

export const sendWebMessage = createServerFn({ method: "POST" })
  .validator(z.object({ text: z.string().trim().min(1).max(8000) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const res = await callRuntime((rt) => rt.webSend(data.text))
    const firstRun = getSetting(db, "firstRun")
    if (!firstRun.saidHi) setSetting(db, "firstRun", { ...firstRun, saidHi: true })
    return res
  })

export const decideApproval = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100), approved: z.boolean() }))
  .handler(async ({ data }) => {
    await requireWriter()
    return callRuntime((rt) => rt.approve(data.id, data.approved))
  })

export const stopTurn = createServerFn({ method: "POST" })
  .validator(z.object({ chatId: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    await requireWriter()
    return callRuntime((rt) => rt.stop(data.chatId))
  })
