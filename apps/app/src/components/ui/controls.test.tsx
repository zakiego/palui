// @vitest-environment jsdom
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test } from "vitest"

import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "./accordion"
import { Avatar, AvatarFallback } from "./avatar"
import { Badge } from "./badge"
import { Button } from "./button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./collapsible"
import { Input } from "./input"
import { InputGroup, InputGroupInput } from "./input-group"
import { Item } from "./item"
import { Label } from "./label"
import { Progress } from "./progress"
import { RadioGroup, RadioGroupItem } from "./radio-group"
import { Separator } from "./separator"
import { Switch } from "./switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./tabs"
import { Textarea } from "./textarea"
import { ToggleGroup, ToggleGroupItem } from "./toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "./tooltip"

const agent = createAgent()

test("switch keeps its size and toggles with Space", async () => {
  render(<Switch aria-label="Allowed" size="sm" />)
  const control = screen.getByRole("switch", { name: "Allowed" })
  expect(control.dataset.slot).toBe("switch")
  expect(control.dataset.size).toBe("sm")
  expect(control.getAttribute("aria-checked")).toBe("false")
  control.focus()
  await userEvent.keyboard(" ")
  expect(control.getAttribute("aria-checked")).toBe("true")
  expect(control.hasAttribute("data-checked")).toBe(true)
})

test("separator reports its orientation for the line styles", () => {
  render(<Separator orientation="vertical" />)
  const line = screen.getByRole("separator")
  expect(line.dataset.orientation).toBe("vertical")
  expect(line.className).toContain("data-vertical:w-px")
})

test("progress renders a named bar with the indicator slot callers style", () => {
  render(<Progress value={40} aria-label="Cap used" className="mt-1" />)
  const bar = screen.getByRole("progressbar", { name: "Cap used" })
  expect(bar.getAttribute("aria-valuenow")).toBe("40")
  expect(bar.className).toContain("mt-1")
  expect(bar.querySelector("[data-slot=progress-indicator]")).not.toBeNull()
})

test("avatar shows its fallback without an image", () => {
  render(
    <Avatar className="rounded-lg">
      <AvatarFallback className="rounded-lg">Z</AvatarFallback>
    </Avatar>
  )
  const fallback = screen.getByText("Z")
  expect(fallback.dataset.slot).toBe("avatar-fallback")
  expect(fallback.closest("[data-slot=avatar]")?.getAttribute("data-size")).toBe("default")
})

test("collapsible opens from its trigger, and an agent can find it", async () => {
  render(
    <Collapsible>
      <CollapsibleTrigger>Show steps</CollapsibleTrigger>
      <CollapsibleContent>Step one</CollapsibleContent>
    </Collapsible>
  )
  const trigger = screen.getByRole("button", { name: "Show steps" })
  expect(trigger.getAttribute("aria-expanded")).toBe("false")
  await userEvent.click(trigger)
  expect(trigger.getAttribute("aria-expanded")).toBe("true")
  expect(screen.getByText("Step one").isConnected).toBe(true)

  const labels = agent.snapshot().filter((e) => !e.offPage).map((e) => e.label)
  expect(labels).toContain("Show steps")
})

test("accordion opens an item and turns its chevron from aria-expanded", async () => {
  render(
    <Accordion multiple aria-label="Tools">
      <AccordionItem value="notes">
        <AccordionTrigger>Notes</AccordionTrigger>
        <AccordionContent>Save a note</AccordionContent>
      </AccordionItem>
    </Accordion>
  )
  const trigger = screen.getByRole("button", { name: "Notes" })
  expect(trigger.dataset.slot).toBe("accordion-trigger")
  await userEvent.click(trigger)
  expect(trigger.getAttribute("aria-expanded")).toBe("true")
  expect(screen.getByText("Save a note").isConnected).toBe(true)
})

test("radio group moves its value with the arrow keys", async () => {
  const seen: (string | null)[] = []
  render(
    <RadioGroup aria-label="Which agents" defaultValue="all" onValueChange={(v) => seen.push(v)}>
      <RadioGroupItem value="all" aria-label="All agents" />
      <RadioGroupItem value="one" aria-label="One agent" />
    </RadioGroup>
  )
  const all = screen.getByRole("radio", { name: "All agents" })
  expect(all.getAttribute("aria-checked")).toBe("true")
  all.focus()
  await userEvent.keyboard("{ArrowDown}")
  expect(seen).toEqual(["one"])
  expect(screen.getByRole("radio", { name: "One agent" }).hasAttribute("data-checked")).toBe(true)
})

test("toggle group presses several levels at once", async () => {
  const seen: string[][] = []
  render(
    <ToggleGroup multiple variant="outline" aria-label="Level" onValueChange={(v) => seen.push(v)}>
      <ToggleGroupItem value="info" aria-label="Show info">info</ToggleGroupItem>
      <ToggleGroupItem value="warn" aria-label="Show warn">warn</ToggleGroupItem>
    </ToggleGroup>
  )
  await userEvent.click(screen.getByRole("button", { name: "Show info" }))
  await userEvent.click(screen.getByRole("button", { name: "Show warn" }))
  expect(seen.at(-1)).toEqual(["info", "warn"])
  expect(screen.getByRole("button", { name: "Show warn" }).getAttribute("aria-pressed")).toBe("true")
})

test("line tabs mark the selected tab with data-selected", async () => {
  render(
    <Tabs defaultValue="general">
      <TabsList variant="line" aria-label="Settings">
        <TabsTrigger value="general">General</TabsTrigger>
        <TabsTrigger value="models">Models</TabsTrigger>
      </TabsList>
      <TabsContent value="general">General panel</TabsContent>
      <TabsContent value="models">Models panel</TabsContent>
    </Tabs>
  )
  const models = screen.getByRole("tab", { name: "Models" })
  await userEvent.click(models)
  expect(models.hasAttribute("data-selected")).toBe(true)
  expect(models.closest("[data-slot=tabs-list]")?.getAttribute("data-variant")).toBe("line")
  expect(screen.getByText("Models panel").isConnected).toBe(true)
})

test("tooltip describes its trigger and starts closed", () => {
  render(
    <Tooltip>
      <TooltipTrigger aria-label="Theme">
        <svg aria-hidden="true" />
      </TooltipTrigger>
      <TooltipContent>Switch to dark theme</TooltipContent>
    </Tooltip>
  )
  screen.getByRole("button", { name: "Theme", description: "Switch to dark theme" })
  expect(document.querySelector("[data-slot=tooltip-content]")?.hasAttribute("data-closed")).toBe(true)
})

test("button keeps its variant classes and renders as a link through render", () => {
  render(
    <>
      <Button variant="outline" size="sm">Save</Button>
      <Button variant="ghost" render={<a href="/flows" />}>Back to flows</Button>
    </>
  )
  const save = screen.getByRole("button", { name: "Save" })
  expect(save.dataset.slot).toBe("button")
  expect(save.className).toContain("shadow-(--shadow-border)")
  expect(save.className).toContain("active:not-disabled:scale-[0.96]")
  expect(screen.getByRole("link", { name: "Back to flows" }).getAttribute("href")).toBe("/flows")
})

test("input and textarea report typing through onChange and onValueChange", async () => {
  const typed: string[] = []
  render(
    <>
      <Label htmlFor="name">Name</Label>
      <Input id="name" onValueChange={(v) => typed.push(v)} />
      <Textarea aria-label="Persona" defaultValue="hi" />
      <InputGroup>
        <InputGroupInput aria-label="Search" />
      </InputGroup>
    </>
  )
  await userEvent.type(screen.getByLabelText("Name"), "ab")
  expect(typed.at(-1)).toBe("ab")
  expect(screen.getByLabelText<HTMLTextAreaElement>("Persona").value).toBe("hi")
  expect(screen.getByLabelText("Search").hasAttribute("data-input-group-control")).toBe(true)
})

test("badge and item keep their slots and take a render element", () => {
  render(
    <>
      <Badge variant="success">On</Badge>
      <Item render={<a href="/connectors/whatsapp" />}>WhatsApp</Item>
    </>
  )
  const badge = screen.getByText("On")
  expect(badge.dataset.slot).toBe("badge")
  expect(badge.dataset.variant).toBe("success")
  const item = screen.getByRole("link", { name: "WhatsApp" })
  expect(item.dataset.slot).toBe("item")
})
