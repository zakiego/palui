// @vitest-environment jsdom
import { act, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import * as React from "react"
import { expect, test } from "vitest"

import { ResponsiveSheet } from "@/components/responsive-sheet"
import { SimpleSelect } from "@/components/simple-select"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "./alert-dialog"
import { Button } from "./button"
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "./command"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "./dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./dropdown-menu"
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerViewport,
} from "./message-scroller"
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from "./popover"

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

function Picker({ onChange }: { onChange: (v: string) => void }) {
  const [value, setValue] = React.useState("gpt")
  return (
    <SimpleSelect
      aria-label="Model"
      value={value}
      onChange={(v) => {
        setValue(v)
        onChange(v)
      }}
      options={[
        { value: "gpt", label: "GPT 5" },
        { value: "claude", label: "Claude Opus" },
      ]}
    />
  )
}

test("simple select shows the label, not the value, and an agent can change it", async () => {
  const seen: string[] = []
  render(<Picker onChange={(v) => seen.push(v)} />)
  const trigger = screen.getByRole("combobox", { name: "Model" })
  expect(trigger.textContent).toContain("GPT 5")
  expect(trigger.dataset.slot).toBe("select-trigger")

  const result = await act(() =>
    agent.act({ steps: [{ ref: "Model", action: "setValue", args: ["Claude Opus"] }] })
  )
  expect(result.ok, result.error?.message).toBe(true)
  expect(seen).toEqual(["claude"])
  expect(trigger.textContent).toContain("Claude Opus")
})

test("menu items run their onSelect and the trigger is named for its row", async () => {
  const ran: string[] = []
  render(
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label="Actions for Water the plants" />}>
        …
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Snooze</DropdownMenuLabel>
          <DropdownMenuItem onSelect={() => ran.push("10m")}>Snooze 10 minutes</DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          data-agent-confirm="Cancels the reminder"
          onSelect={() => ran.push("cancel")}
        >
          Cancel reminder
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
  const trigger = screen.getByRole("button", { name: "Actions for Water the plants" })
  await userEvent.click(trigger)
  const snooze = await screen.findByRole("menuitem", { name: "Snooze 10 minutes" })
  snooze.focus()
  await userEvent.keyboard("{Enter}")
  expect(ran).toEqual(["10m"])

  const cancel = {
    steps: [{ ref: "Actions for Water the plants", action: "press", args: ["Cancel reminder"] }],
  }
  // In jsdom a closed menu's items aren't there to press, so open it first.
  await userEvent.click(trigger)
  expect((await act(() => agent.act(cancel))).ok).toBe(false)
  expect(ran).toEqual(["10m"])
  approve = true
  if (!screen.queryByRole("menuitem", { name: "Cancel reminder" })) await userEvent.click(trigger)
  const result = await act(() => agent.act(cancel))
  expect(result.ok, result.error?.message).toBe(true)
  expect(ran).toEqual(["10m", "cancel"])
  approve = false
})

test("popover opens from its trigger and is named by its title", async () => {
  render(
    <Popover>
      <PopoverTrigger>Pick a model</PopoverTrigger>
      <PopoverContent align="start">
        <PopoverTitle>Models</PopoverTitle>
      </PopoverContent>
    </Popover>
  )
  await userEvent.click(screen.getByRole("button", { name: "Pick a model" }))
  const dialog = await screen.findByRole("dialog", { name: "Models" })
  expect(dialog.dataset.slot).toBe("popover-content")
  expect(dialog.hasAttribute("data-open")).toBe(true)
})

test("alert dialog: Cancel closes it, the action runs, and the trigger isn't registered twice", async () => {
  const removed: string[] = []
  render(
    <AlertDialog>
      <AlertDialogTrigger render={<Button variant="destructive">Delete note</Button>} />
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this note?</AlertDialogTitle>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep it</AlertDialogCancel>
          <AlertDialogAction variant="danger" onClick={() => removed.push("note")}>Delete forever</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
  const named = agent.snapshot().filter((e) => !e.offPage && e.label === "Delete note")
  expect(named).toHaveLength(1)

  const trigger = screen.getByRole("button", { name: "Delete note" })
  await userEvent.click(trigger)
  await screen.findByRole("alertdialog", { name: "Delete this note?" })
  await userEvent.click(screen.getByRole("button", { name: "Keep it" }))
  expect(screen.queryByRole("alertdialog")).toBeNull()

  await userEvent.click(trigger)
  await userEvent.click(await screen.findByRole("button", { name: "Delete forever" }))
  expect(removed).toEqual(["note"])
})

test("dialog is named by its title and closes from its icon button", async () => {
  render(
    <Dialog defaultOpen>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Upload a file</DialogTitle>
        </DialogHeader>
      </DialogContent>
    </Dialog>
  )
  await screen.findByRole("dialog", { name: "Upload a file" })
  await userEvent.click(screen.getByRole("button", { name: "Close" }))
  expect(screen.queryByRole("dialog")).toBeNull()
})

test("responsive sheet opens as a side sheet on a wide screen", async () => {
  render(
    <ResponsiveSheet open onOpenChange={() => {}} title="WhatsApp" footer={<Button>Save</Button>}>
      <p>Linked device</p>
    </ResponsiveSheet>
  )
  const sheet = await screen.findByRole("dialog", { name: "WhatsApp" })
  expect(sheet.dataset.side).toBe("right")
  expect(screen.getByText("Linked device").isConnected).toBe(true)
})

test("command dialog filters by an item's value too and runs it with Enter", async () => {
  const ran: string[] = []
  render(
    <CommandDialog open onOpenChange={() => {}} title="Search" description="Type a command">
      <Command>
        <CommandInput placeholder="Type a command or search" />
        <CommandEmpty>Nothing found</CommandEmpty>
        <CommandList>
          <CommandGroup heading="Connectors">
            <CommandItem value="turn off whatsapp WhatsApp" onSelect={() => ran.push("whatsapp")}>
              turn off whatsapp
            </CommandItem>
            <CommandItem value="turn off telegram Telegram" onSelect={() => ran.push("telegram")}>
              turn off telegram
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </CommandDialog>
  )
  await screen.findByRole("dialog", { name: "Search" })
  const input = screen.getByRole("combobox", { name: "Type a command or search" })
  await userEvent.type(input, "Telegram")
  expect(screen.queryByRole("option", { name: /whatsapp/ })).toBeNull()
  await userEvent.keyboard("{Enter}")
  expect(ran).toEqual(["telegram"])
})

test("message scroller renders its items inside a sticky scroll area", () => {
  render(
    <MessageScroller className="h-40">
      <MessageScrollerViewport>
        <MessageScrollerContent>
          <MessageScrollerItem>Hi</MessageScrollerItem>
        </MessageScrollerContent>
      </MessageScrollerViewport>
      <MessageScrollerButton />
    </MessageScroller>
  )
  const root = screen.getByText("Hi").closest("[data-slot=scroll-area]")
  expect(root).not.toBeNull()
  expect(document.querySelector("[data-slot=scroll-area-scroll-to-end]")?.getAttribute("aria-label")).toBe("Scroll to end")
})
