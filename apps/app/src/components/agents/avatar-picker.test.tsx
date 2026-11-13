// @vitest-environment jsdom
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"

import { AVATAR_ICONS } from "@/components/agent-avatar"
import { AvatarPicker } from "./avatar-picker"

const agent = { isDefault: false, avatarImage: null, avatarIcon: "bot", avatarColor: null }

test("icon and color pickers are radio groups with one tab stop and arrow keys", async () => {
  const onIcon = vi.fn()
  const onColor = vi.fn()
  render(<AvatarPicker agent={agent} onImage={() => {}} onIcon={onIcon} onColor={onColor} />)

  const groups = screen.getAllByRole("radiogroup")
  expect(groups).toHaveLength(2)
  const names = Object.keys(AVATAR_ICONS)
  const checked = screen.getByRole("radio", { name: "bot", checked: true })
  expect(checked.tabIndex).toBe(0)
  expect(screen.getAllByRole("radio").filter((r) => r.tabIndex === 0)).toHaveLength(2)

  checked.focus()
  await userEvent.keyboard("{ArrowRight}")
  expect(onIcon).toHaveBeenCalledWith(names[names.indexOf("bot") + 1])

  await userEvent.click(screen.getByRole("radio", { name: "violet" }))
  expect(onColor).toHaveBeenCalledWith("violet")
})
