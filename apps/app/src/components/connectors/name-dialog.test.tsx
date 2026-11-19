// @vitest-environment jsdom
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"

import { manifestFor } from "@/lib/manifests"
import { NameDialog } from "./name-dialog"

const http = manifestFor("http")!

test("asks for a name, shows the id it will get, and submits with Enter", async () => {
  const onSubmit = vi.fn()
  render(<NameDialog manifest={http} existing={[{ id: "http-elevenlabs" }]} onSubmit={onSubmit} onClose={() => {}} />)

  const input = screen.getByRole("textbox", { name: "Name" })
  expect(input).toHaveProperty("maxLength", 80)
  const go = screen.getByRole("button", { name: "Continue" })
  expect(go).toHaveProperty("disabled", true)

  await userEvent.type(input, "ElevenLabs")
  expect(screen.getByText(/http-elevenlabs-2__http_request/)).toBeTruthy()
  expect(go).toHaveProperty("disabled", false)

  await userEvent.keyboard("{Enter}")
  expect(onSubmit).toHaveBeenCalledWith("ElevenLabs", "http-elevenlabs-2")
})

test("an empty name doesn't submit, and Cancel closes", async () => {
  const onSubmit = vi.fn()
  const onClose = vi.fn()
  render(<NameDialog manifest={http} existing={[]} onSubmit={onSubmit} onClose={onClose} />)

  await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "   {Enter}")
  expect(onSubmit).not.toHaveBeenCalled()

  await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
  expect(onClose).toHaveBeenCalled()
})
