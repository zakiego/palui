import { describe, expect, it } from "vitest"
import { z } from "zod"
import { describeField, type FieldStep, wizardFieldInfo, wizardInitialValues, wizardPayload } from "./zod-fields"

// Shaped like the WhatsApp manifest's confirmation step, inline so this test
// doesn't depend on the manifest change.
const config = z.object({
  utusanCleared: z.boolean().default(false),
  name: z.string().optional(),
})
const step: FieldStep = { field: "utusanCleared", label: "This chat isn't bound in utusan", help: "Unbind it in utusan first." }
const manifest = {
  config,
  secrets: { token: z.string() },
  setup: [step, { field: "name", label: "Name" }, { field: "token", label: "Token" }, { action: "pairWhatsApp" as const, label: "Pair" }],
}

describe("boolean config fields in the Saklar wizard", () => {
  it("describes z.boolean().default(false) as an optional boolean defaulting to false", () => {
    expect(describeField(z.boolean().default(false))).toEqual({ kind: "boolean", optional: true, defaultValue: false })
    expect(describeField(z.boolean())).toEqual({ kind: "boolean", optional: false, defaultValue: undefined })
  })

  it("renders it as a boolean field, not a secret", () => {
    expect(wizardFieldInfo(manifest, step)).toMatchObject({ kind: "boolean", secret: false })
  })

  it("starts from the schema default, then from the stored value", () => {
    expect(wizardInitialValues(manifest, {})).toEqual({ utusanCleared: false, name: "", token: "" })
    expect(wizardInitialValues(manifest, { utusanCleared: true })).toMatchObject({ utusanCleared: true })
  })

  it("saves true and false as booleans that the schema accepts", () => {
    const on = wizardPayload(manifest, { utusanCleared: true, name: " x ", token: "" })
    expect(on).toEqual({ config: { utusanCleared: true, name: "x" }, secrets: {} })
    const off = wizardPayload(manifest, { utusanCleared: false, name: "", token: "t" })
    expect(off.config.utusanCleared).toBe(false)
    expect(off.secrets).toEqual({ token: "t" })
    expect(config.shape.utusanCleared.safeParse(on.config.utusanCleared)).toEqual({ success: true, data: true })
    expect(config.shape.utusanCleared.safeParse(off.config.utusanCleared)).toEqual({ success: true, data: false })
  })
})
