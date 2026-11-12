// The response for a saved attachment's bytes, shared by the signed-in route
// (/api/attachments/:id) and share links (/api/share/:token).
//
// A download is always safe. Inline is only for what previewKind allows
// (JPEG, PNG, WebP, GIF, PDF, plain text, CSV), and always with nosniff, so a
// file can never render as an HTML page on the dashboard's origin. Text goes out
// as text/plain whatever it was saved as. Images and text also get a sandbox
// CSP. PDFs can't: Chrome's viewer shows a blank page under `sandbox`, so they
// get `default-src 'none'` and the PDF viewer's own isolation instead.
import { downloadName, previewKind, type AttachmentMeta } from "@palui/core/attachments/service"

export function fileResponse(meta: AttachmentMeta, bytes: Buffer, opts: { inline: boolean; cache?: string }): Response {
  const name = downloadName(meta)
  const ascii = name.replace(/[^\x20-\x7e]/g, "_").replace(/"/g, "")
  const kind = opts.inline ? previewKind(meta.mimeType) : null
  const disposition = kind ? "inline" : "attachment"
  return new Response(new Uint8Array(bytes), {
    headers: {
      "content-type": kind === "text" ? "text/plain; charset=utf-8" : meta.mimeType,
      "content-length": String(bytes.length),
      "content-disposition": `${disposition}; filename="${ascii}"; filename*=UTF-8''${encodeURIComponent(name)}`,
      "x-content-type-options": "nosniff",
      "content-security-policy": kind === "pdf" ? "default-src 'none'; frame-ancestors 'self'" : "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox; frame-ancestors 'self'",
      "referrer-policy": "no-referrer",
      "cache-control": opts.cache ?? "private, no-store",
    },
  })
}
