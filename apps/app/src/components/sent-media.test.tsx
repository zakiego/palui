// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, expect, test } from "vitest"

import type { SentMediaView } from "@/lib/api/chats"
import { SentMedia } from "./sent-media"

afterEach(cleanup)

const media: SentMediaView[] = [
  { id: "out_1", kind: "image", mimeType: "image/png", filename: "inhaler.png", size: 9_876_543, caption: "Inhaler + spacer", status: "sent", error: null },
  { id: "out_2", kind: "voice", mimeType: "audio/ogg", filename: "", size: 48_213, caption: "", status: "pending", error: null },
  { id: "out_3", kind: "document", mimeType: "application/pdf", filename: "jadwal.pdf", size: 1000, caption: "", status: "failed", error: "413: Request Entity Too Large" },
]

test("lists each sent file with its kind, size, caption and delivery status", () => {
  render(<SentMedia media={media} />)
  expect(screen.getAllByRole("listitem")).toHaveLength(3)
  expect(screen.getByText("inhaler.png")).toBeTruthy()
  expect(screen.getByText("9.4 MB")).toBeTruthy()
  expect(screen.getByText("Inhaler + spacer")).toBeTruthy()
  expect(screen.getByText("Delivered")).toBeTruthy()
  // No file name: the kind stands in.
  expect(screen.getAllByText(/Voice note/).length).toBeGreaterThan(0)
  expect(screen.getByText("Waiting")).toBeTruthy()
  expect(screen.getByText("Not sent")).toBeTruthy()
  expect(screen.getByText("413: Request Entity Too Large")).toBeTruthy()
})

test("renders nothing without media", () => {
  const { container } = render(<SentMedia media={[]} />)
  expect(container.innerHTML).toBe("")
})
