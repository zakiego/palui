import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { RpcError, notifyUnauthorized, onUnauthorized, rpc, rpcUrl } from "./client"
import { getTranscript } from "./chats"
import { dismissChecklist, getShell } from "./home"
import { saveConnectorDraft } from "./connectors"
import { getEvents } from "./activity"
import { redirectToLoginOnUnauthorized } from "@/lib/session-redirect"

type Call = { url: string; init: RequestInit }

let calls: Call[]
let next: () => Response

beforeEach(() => {
  calls = []
  next = () => Response.json({ ok: true })
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    calls.push({ url, init })
    return next()
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("rpc encoding", () => {
  it("sends a GET's input as a JSON query parameter", async () => {
    next = () => Response.json({ events: [], total: 0, connectors: [], timezone: "Asia/Makassar" })
    const res = await getEvents({ data: { connectorId: "telegram", levels: ["warn", "error"] } })
    expect(res.timezone).toBe("Asia/Makassar")
    expect(calls).toHaveLength(1)
    const url = new URL(calls[0]!.url, "http://x")
    expect(url.pathname).toBe("/api/rpc/getEvents")
    expect(JSON.parse(url.searchParams.get("input")!)).toEqual({ connectorId: "telegram", levels: ["warn", "error"] })
    expect(calls[0]!.init.method).toBe("GET")
    expect(calls[0]!.init.body).toBeUndefined()
    expect(calls[0]!.init.credentials).toBe("same-origin")
  })

  it("leaves the query off a GET without input", async () => {
    await getShell()
    expect(calls[0]!.url).toBe("/api/rpc/getShell")
    expect(calls[0]!.init.method).toBe("GET")
  })

  it("encodes characters that need it", () => {
    expect(rpcUrl("getTranscript", "GET", { chatId: "a&b=c #?" })).toBe(`/api/rpc/getTranscript?input=${encodeURIComponent('{"chatId":"a&b=c #?"}')}`)
  })

  it("sends a POST's input as a JSON body", async () => {
    next = () => Response.json({ ok: true })
    await saveConnectorDraft({ data: { id: "mcp-x", type: "mcp", config: { url: "https://x.dev" }, agentId: null } })
    expect(calls[0]!.url).toBe("/api/rpc/saveConnectorDraft")
    expect(calls[0]!.init.method).toBe("POST")
    expect((calls[0]!.init.headers as Record<string, string>)["content-type"]).toBe("application/json")
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ id: "mcp-x", type: "mcp", config: { url: "https://x.dev" }, agentId: null })
  })

  it("sends no body for a POST without input", async () => {
    await dismissChecklist()
    expect(calls[0]!.init.method).toBe("POST")
    expect(calls[0]!.init.body).toBeUndefined()
  })

  it("returns null for an empty 200", async () => {
    next = () => new Response("", { status: 200 })
    expect(await rpc("x", "GET")).toBeNull()
  })
})

describe("rpc errors", () => {
  it("throws the server's message, as a server function's error was", async () => {
    next = () => Response.json({ error: "That chat doesn't exist anymore.", code: "BAD_REQUEST" }, { status: 400 })
    const err = await getTranscript({ data: { chatId: "c1" } }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(Error)
    expect(err).toBeInstanceOf(RpcError)
    expect((err as Error).message).toBe("That chat doesn't exist anymore.")
    expect((err as RpcError).status).toBe(400)
    expect((err as RpcError).code).toBe("BAD_REQUEST")
  })

  it("keeps field errors and zod issues", async () => {
    next = () => Response.json({ error: "Some fields need a fix.", code: "BAD_REQUEST", fields: { url: "Invalid URL" }, issues: [{ path: ["url"] }] }, { status: 400 })
    const err = (await rpc("saveConnectorDraft", "POST", {}).catch((e: unknown) => e)) as RpcError
    expect(err.fields).toEqual({ url: "Invalid URL" })
    expect(err.issues).toEqual([{ path: ["url"] }])
  })

  it("reads an { error: { message } } body too", async () => {
    next = () => Response.json({ error: { message: "Nope." } }, { status: 409 })
    await expect(rpc("x", "POST", {})).rejects.toThrow("Nope.")
  })

  it("falls back to the status when the body isn't JSON", async () => {
    next = () => new Response("<html>bad gateway</html>", { status: 502, statusText: "Bad Gateway" })
    await expect(rpc("x", "GET")).rejects.toThrow("502 Bad Gateway")
  })

  it("rejects when the network fails", async () => {
    vi.stubGlobal("fetch", async () => {
      throw new TypeError("Failed to fetch")
    })
    await expect(rpc("x", "GET")).rejects.toThrow("Failed to fetch")
  })
})

describe("401", () => {
  it("throws UNAUTHORIZED and tells the listeners", async () => {
    const seen = vi.fn()
    const off = onUnauthorized(seen)
    next = () => Response.json({ error: "UNAUTHORIZED", code: "UNAUTHORIZED" }, { status: 401 })
    const err = (await getShell().catch((e: unknown) => e)) as RpcError
    off()
    // AppError matches this text to go to /login.
    expect(err.message).toBe("UNAUTHORIZED")
    expect(err.status).toBe(401)
    expect(seen).toHaveBeenCalledTimes(1)
  })

  it("is UNAUTHORIZED even with an empty body", async () => {
    next = () => new Response("", { status: 401 })
    await expect(rpc("x", "GET")).rejects.toThrow("UNAUTHORIZED")
  })

  it("sends the tab to the login page, coming back afterwards", async () => {
    const navigate = vi.fn(async () => {})
    const router = { state: { matches: [{ routeId: "__root__" }, { routeId: "/_app" }, { routeId: "/_app/connectors" }], location: { href: "/connectors?v=2" } }, navigate }
    const off = redirectToLoginOnUnauthorized(router)
    next = () => Response.json({ error: "UNAUTHORIZED", code: "UNAUTHORIZED" }, { status: 401 })
    await getShell().catch(() => {})
    off()
    expect(navigate).toHaveBeenCalledWith({ to: "/login", search: { next: "/connectors?v=2" } })
  })

  it("sends the tab once when the page's calls and the stream fail together", async () => {
    // The router moves location at once and matches only after loading, so the
    // second 401 sees the login URL with the old page's matches.
    let finish = () => {}
    const router = {
      state: { matches: [{ routeId: "__root__" }, { routeId: "/_app" }, { routeId: "/_app/activity" }], location: { href: "/activity" } },
      navigate: vi.fn((opts: { to: "/login"; search: { next: string } }) => {
        router.state.location = { href: `/login?next=${encodeURIComponent(opts.search.next)}` }
        return new Promise<void>((resolve) => {
          finish = resolve
        })
      }),
    }
    const off = redirectToLoginOnUnauthorized(router)
    notifyUnauthorized()
    notifyUnauthorized()
    finish()
    await Promise.resolve()
    notifyUnauthorized()
    off()
    expect(router.navigate).toHaveBeenCalledTimes(1)
    expect(router.navigate).toHaveBeenCalledWith({ to: "/login", search: { next: "/activity" } })
  })

  it("leaves the login and invite pages alone", () => {
    for (const routeId of ["/login", "/invite/$token"]) {
      const navigate = vi.fn(async () => {})
      const off = redirectToLoginOnUnauthorized({ state: { matches: [{ routeId: "__root__" }, { routeId }], location: { href: "/login" } }, navigate })
      notifyUnauthorized()
      off()
      expect(navigate).not.toHaveBeenCalled()
    }
  })
})
