import { describe, expect, it } from "vitest"
import { assertPublicUrl, blockedAddressReason, isBlockedAddress, resolvePublic, SsrfError, type Resolver } from "./ssrf"

describe("blockedAddressReason", () => {
  it.each([
    "127.0.0.1",
    "127.1.2.3",
    "10.0.0.1",
    "172.16.0.1",
    "172.31.255.255",
    "192.168.1.1",
    "169.254.169.254",
    "100.64.0.1",
    "100.127.255.255",
    "0.0.0.0",
    "0.1.2.3",
    "224.0.0.1",
    "239.255.255.250",
    "255.255.255.255",
    "::1",
    "::",
    "fc00::1",
    "fd12:3456::1",
    "fe80::1",
    "fe80::1%eth0",
    "ff02::1",
    "::ffff:127.0.0.1",
    "::ffff:7f00:1",
    "::ffff:10.0.0.1",
    "::ffff:169.254.169.254",
    "::127.0.0.1",
    "64:ff9b::a9fe:a9fe",
    "2002:7f00:1::",
    "[::1]",
    // IPv4-translated, local-use NAT64 and Teredo.
    "::ffff:0:127.0.0.1",
    "::ffff:0:10.0.0.1",
    "::ffff:0:a9fe:a9fe",
    "64:ff9b:1::a9fe:a9fe",
    "64:ff9b:1:abcd::808:808",
    "2001::1",
    "2001:0:4136:e378:8000:63bf:3fff:fdd2",
  ])("blocks %s", (ip) => {
    expect(isBlockedAddress(ip)).toBe(true)
  })

  it.each(["1.1.1.1", "8.8.8.8", "172.32.0.1", "100.128.0.1", "93.184.216.34", "2606:4700:4700::1111", "::ffff:8.8.8.8", "2002:808:808::", "::ffff:0:8.8.8.8", "2001:4860:4860::8888"])(
    "allows %s",
    (ip) => {
      expect(blockedAddressReason(ip)).toBeNull()
    },
  )

  it("treats garbage as blocked", () => {
    expect(isBlockedAddress("example.com")).toBe(true)
    expect(isBlockedAddress("1.2.3")).toBe(true)
  })
})

describe("assertPublicUrl", () => {
  const code = (u: string) => {
    try {
      assertPublicUrl(u)
      return "ok"
    } catch (e) {
      return (e as SsrfError).code
    }
  }

  it("allows public http(s) urls", () => {
    expect(code("https://example.com/a?b=1")).toBe("ok")
    expect(code("http://example.com:8080/")).toBe("ok")
  })

  it("refuses other schemes and credentials", () => {
    expect(code("file:///etc/passwd")).toBe("blocked_protocol")
    expect(code("ftp://example.com/")).toBe("blocked_protocol")
    expect(code("gopher://example.com/")).toBe("blocked_protocol")
    expect(code("https://user:pass@example.com/")).toBe("blocked_credentials")
    expect(code("not a url")).toBe("invalid_url")
  })

  it("refuses internal names and literal internal addresses", () => {
    expect(code("http://localhost/")).toBe("blocked_host")
    expect(code("http://api.localhost/")).toBe("blocked_host")
    expect(code("http://host.docker.internal:4800/")).toBe("blocked_host")
    expect(code("http://printer.local/")).toBe("blocked_host")
    expect(code("http://router/")).toBe("blocked_host")
    expect(code("http://127.0.0.1/")).toBe("blocked_address")
    expect(code("http://169.254.169.254/latest/meta-data")).toBe("blocked_address")
    expect(code("http://[::1]/")).toBe("blocked_address")
    expect(code("http://[::ffff:127.0.0.1]/")).toBe("blocked_address")
  })

  it("catches numeric host tricks the URL parser normalizes", () => {
    expect(code("http://2130706433/")).toBe("blocked_address")
    expect(code("http://0x7f.0.0.1/")).toBe("blocked_address")
    expect(code("http://0177.0.0.1/")).toBe("blocked_address")
    expect(code("http://127.1/")).toBe("blocked_address")
  })

  it("refuses non-HTTP service ports", () => {
    expect(code("http://example.com:22/")).toBe("blocked_port")
    expect(code("http://example.com:6379/")).toBe("blocked_port")
    expect(code("http://example.com:25/")).toBe("blocked_port")
  })
})

describe("resolvePublic", () => {
  const resolver =
    (map: Record<string, string[]>): Resolver =>
    async (h) => {
      const rows = map[h]
      if (!rows) throw new Error("ENOTFOUND")
      return rows.map((address) => ({ address, family: address.includes(":") ? 6 : 4 }))
    }

  it("returns the first address of a public host", async () => {
    await expect(resolvePublic("example.com", resolver({ "example.com": ["93.184.216.34"] }))).resolves.toEqual({ address: "93.184.216.34", family: 4 })
  })

  it("refuses a host that resolves to an internal address", async () => {
    await expect(resolvePublic("evil.test", resolver({ "evil.test": ["127.0.0.1"] }))).rejects.toMatchObject({ code: "blocked_address" })
  })

  it("refuses a host when any address is internal", async () => {
    await expect(resolvePublic("mixed.test", resolver({ "mixed.test": ["93.184.216.34", "10.0.0.5"] }))).rejects.toMatchObject({ code: "blocked_address" })
  })

  it("refuses IPv4-mapped IPv6 answers", async () => {
    await expect(resolvePublic("v6.test", resolver({ "v6.test": ["::ffff:192.168.0.1"] }))).rejects.toMatchObject({ code: "blocked_address" })
  })

  it("reports DNS failures", async () => {
    await expect(resolvePublic("nope.test", resolver({}))).rejects.toMatchObject({ code: "dns" })
    await expect(resolvePublic("empty.test", resolver({ "empty.test": [] }))).rejects.toMatchObject({ code: "dns" })
  })
})
