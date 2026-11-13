// @vitest-environment jsdom
import { render } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { splitTemplate, TemplateText } from "./template-text"

describe("splitTemplate", () => {
  it("marks each {{placeholder}} and keeps the text around it", () => {
    expect(splitTemplate("🕌 {{trigger.label}} at {{ time }}")).toEqual([
      { text: "🕌 ", placeholder: false },
      { text: "{{trigger.label}}", placeholder: true },
      { text: " at ", placeholder: false },
      { text: "{{ time }}", placeholder: true },
    ])
  })

  it("leaves plain text and unclosed braces alone", () => {
    expect(splitTemplate("no vars")).toEqual([{ text: "no vars", placeholder: false }])
    expect(splitTemplate("open {{ only")).toEqual([{ text: "open {{ only", placeholder: false }])
  })
})

describe("TemplateText", () => {
  it("renders placeholders as their own element and keeps the full text", () => {
    const { container } = render(
      <p>
        <TemplateText text="🕌 {{trigger.label}}" />
      </p>,
    )
    expect(container.textContent).toBe("🕌 {{trigger.label}}")
    expect(container.querySelectorAll("span")).toHaveLength(1)
    expect(container.querySelector("span")!.textContent).toBe("{{trigger.label}}")
  })
})
