import { z } from "zod"
import { defineManifest } from "../define"

// The form's copy of the server's domain rules (internal/connectors/httpreq):
// "*", or an optional "*." plus a host name of at least two labels whose last
// label isn't all digits. Split on commas and whitespace, at most 20.
const hostRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$/

export function validDomains(s: string): boolean {
  const entries = [...new Set(s.split(/[,\s]+/).filter(Boolean).map((e) => e.toLowerCase().replace(/\.$/, "")))]
  if (entries.length === 0 || entries.length > 20) return false
  return entries.every((entry) => {
    if (entry === "*") return true
    const host = entry.startsWith("*.") ? entry.slice(2) : entry
    const labels = host.split(".")
    return hostRe.test(host) && !/^[0-9]+$/.test(labels[labels.length - 1])
  })
}

const controlChars = /[\u0000-\u001f\u007f]/

export const httpConfig = z.object({
  domains: z.string().trim().min(1).max(1000).refine(validDomains, "Use host names like api.example.com or *.example.com, separated by commas"),
  auth: z.enum(["header", "query", "none"]).default("header"),
  headerName: z
    .string()
    .trim()
    .min(1)
    .max(100)
    .regex(/^[A-Za-z0-9!#$%&'*+.^_`|~-]+$/, "Header names use letters, digits and dashes")
    .default("Authorization"),
  valueTemplate: z
    .string()
    .trim()
    .min(1)
    .max(200)
    .refine((s) => s.includes("{value}") && !controlChars.test(s), "Put {value} where the key goes")
    .default("Bearer {value}"),
  queryParam: z
    .string()
    .trim()
    .min(1)
    .max(100)
    .regex(/^[A-Za-z0-9_.~-]+$/, "Parameter names use letters, digits, dots, dashes and underscores")
    .optional(),
  policy: z.enum(["ask", "allow"]).default("ask"),
})

export default defineManifest({
  id: "http",
  kind: "tools",
  name: "HTTP API",
  icon: "key-round",
  summary: "Call an HTTP API with a key Palui keeps and never shows the model.",
  multiple: true,
  trust: "external",
  config: httpConfig,
  secrets: {
    value: z
      .string()
      .min(8)
      .max(4000)
      .refine((s) => !controlChars.test(s), "The key can't contain line breaks")
      .optional(),
  },
  setup: [
    {
      field: "domains",
      label: "Allowed domains",
      placeholder: "api.example.com",
      help: "Hosts this connector may call, separated by commas. *.example.com covers subdomains. The key is never sent anywhere else.",
    },
    { field: "auth", label: "How the key is sent", askFirst: true, help: "header: a request header. query: a URL parameter. none: no key." },
    { field: "headerName", label: "Header name", askFirst: true, placeholder: "Authorization", showIf: { field: "auth", in: ["header"] } },
    {
      field: "valueTemplate",
      label: "Header value",
      askFirst: true,
      placeholder: "Bearer {value}",
      help: "{value} is replaced with the key.",
      showIf: { field: "auth", in: ["header"] },
    },
    { field: "queryParam", label: "Query parameter", askFirst: true, placeholder: "api_key", showIf: { field: "auth", in: ["query"] } },
    { field: "value", label: "Key", secret: true, optional: true, askFirst: true, showIf: { field: "auth", in: ["header", "query"] } },
    {
      field: "policy",
      label: "Approval",
      askFirst: true,
      help: "ask: every call waits for your yes. allow: reads run on their own, writes follow the agent's tool permissions.",
    },
  ],
  removeWarning: "This removes the key from Palui. The service isn't touched.",
  docs: {
    notes: [
      "Tools are namespaced <connector id>__<tool>, for example http-elevenlabs__http_request.",
      "The key is sent only to the allowed domains, over https, and is removed from everything the model sees.",
      "Shared chats never get these tools.",
    ],
  },
})
