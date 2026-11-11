// Per-chat queue (PLAN 8.3): one turn at a time per chat. Messages that arrive
// while a turn runs are batched into the next turn. Other work for the chat
// (approval resumes, jobs, hooks) runs in order between turns.
export type QueueItem = { kind: "message"; messageId: string } | { kind: "task"; run: () => Promise<void> }

type ChatState = { running: boolean; items: QueueItem[] }

export type ChatQueue = ReturnType<typeof createChatQueue>

export function createChatQueue(runTurn: (chatId: string, messageIds: string[]) => Promise<void>, onError: (chatId: string, e: unknown) => void) {
  const chats = new Map<string, ChatState>()
  const idleWaiters = new Set<() => void>()
  let paused = false

  function checkIdle() {
    if ([...chats.values()].some((c) => c.running || c.items.length)) return
    for (const w of idleWaiters) w()
    idleWaiters.clear()
  }

  async function drain(chatId: string, st: ChatState) {
    st.running = true
    try {
      while (st.items.length && !paused) {
        const first = st.items[0]!
        if (first.kind === "message") {
          // Take every message waiting at the head: they become one turn.
          const ids: string[] = []
          while (st.items[0]?.kind === "message") ids.push((st.items.shift() as { messageId: string }).messageId)
          try {
            await runTurn(chatId, ids)
          } catch (e) {
            onError(chatId, e)
          }
        } else {
          st.items.shift()
          try {
            await first.run()
          } catch (e) {
            onError(chatId, e)
          }
        }
      }
    } finally {
      st.running = false
      if (!st.items.length) chats.delete(chatId)
      checkIdle()
    }
  }

  function push(chatId: string, item: QueueItem) {
    let st = chats.get(chatId)
    if (!st) {
      st = { running: false, items: [] }
      chats.set(chatId, st)
    }
    if (item.kind === "message" && st.items.some((i) => i.kind === "message" && i.messageId === item.messageId)) return
    st.items.push(item)
    if (!st.running && !paused) void drain(chatId, st)
  }

  // Run a task in the chat's order and wait for its result.
  function runInChat<T>(chatId: string, fn: () => Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      push(chatId, {
        kind: "task",
        run: async () => {
          try {
            resolve(await fn())
          } catch (e) {
            reject(e)
          }
        },
      })
    })
  }

  return {
    push,
    runInChat,
    busy: (chatId: string) => !!chats.get(chatId)?.running,
    // Stop starting new work (drain on shutdown). Running items finish.
    pause() {
      paused = true
    },
    idle(): Promise<void> {
      return new Promise((resolve) => {
        idleWaiters.add(resolve)
        checkIdle()
      })
    },
    // Whether anything is running (shutdown waits for this).
    active(): boolean {
      return [...chats.values()].some((c) => c.running)
    },
  }
}
