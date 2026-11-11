// Where the public OAuth callback sends the browser afterwards. The detail can
// carry text from the query string (Google's `error`), so it's cut down to one
// short line: control and bidi characters removed, whitespace collapsed, at
// most 200 characters. The target is always APP_URL's Connectors page.
export const DETAIL_MAX = 200

// C0, DEL, C1, and the bidi embedding, override and isolate marks.
const UNSAFE = /[\u0000-\u001f\u007f-\u009f‎‏‪-‮⁦-⁩]/g

export function cleanDetail(detail: string): string {
  return Array.from(detail.replace(UNSAFE, " ").replace(/\s+/g, " ").trim())
    .slice(0, DETAIL_MAX)
    .join("")
}

export function oauthReturnUrl(appUrl: string, result: { ok: boolean; detail: string }): string {
  const target = new URL(`${appUrl.replace(/\/+$/, "")}/app/connectors`)
  target.searchParams.set("oauth", result.ok ? "ok" : "error")
  target.searchParams.set("detail", cleanDetail(result.detail))
  return target.toString()
}
