import { describe, expect, it } from "vitest"
import { z } from "zod"
import { describeField, type FieldStep, wizardFieldInfo, wizardInitialValues, wizardPayload } from "./zod-fields"

// A boolean wizard step, inline so this test doesn't depend on any manifest.
const config = z.object({
  notify: z.boolean().default(false),
  name: z.string().optional(),
})
const step: FieldStep = { field: "notify", label: "Send notifications", help: "Off keeps it quiet." }
const manifest = {
  config,
  secrets: { token: z.string() },
  setup: [step, { field: "name", label: "Name" }, { field: "token", label: "Token" }, { action: "pairWhatsApp" as const, label: "Pair" }],
}

describe("boolean config fields in the connector wizard", () => {
  it("describes z.boolean().default(false) as an optional boolean defaulting to false", () => {
    expect(describeField(z.boolean().default(false))).toEqual({ kind: "boolean", optional: true, defaultValue: false })
    expect(describeField(z.boolean())).toEqual({ kind: "boolean", optional: false, defaultValue: undefined })
  })

  it("renders it as a boolean field, not a secret", () => {
    expect(wizardFieldInfo(manifest, step)).toMatchObject({ kind: "boolean", secret: false })
  })

  it("starts from the schema default, then from the stored value", () => {
    expect(wizardInitialValues(manifest, {})).toEqual({ notify: false, name: "", token: "" })
    expect(wizardInitialValues(manifest, { notify: true })).toMatchObject({ notify: true })
  })

  it("saves true and false as booleans that the schema accepts", () => {
    const on = wizardPayload(manifest, { notify: true, name: " x ", token: "" })
    expect(on).toEqual({ config: { notify: true, name: "x" }, secrets: {} })
    const off = wizardPayload(manifest, { notify: false, name: "", token: "t" })
    expect(off.config.notify).toBe(false)
    expect(off.secrets).toEqual({ token: "t" })
    expect(config.shape.notify.safeParse(on.config.notify)).toEqual({ success: true, data: true })
    expect(config.shape.notify.safeParse(off.config.notify)).toEqual({ success: true, data: false })
  })

  it("sends a number box's text when it isn't a number (JSON has no NaN)", () => {
    const m = { config: z.object({ port: z.number().int().optional() }), secrets: {}, setup: [{ field: "port", label: "Port" }] }
    expect(wizardPayload(m, { port: "8080" }).config).toEqual({ port: 8080 })
    expect(wizardPayload(m, { port: "" }).config).toEqual({ port: "" })
    const junk = wizardPayload(m, { port: " 80a " }).config
    expect(junk).toEqual({ port: "80a" })
    expect(JSON.parse(JSON.stringify(junk))).toEqual(junk)
    expect(m.config.shape.port.safeParse(junk.port).success).toBe(false)
  })
})
