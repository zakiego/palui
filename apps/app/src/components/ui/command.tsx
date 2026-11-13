"use client"

import * as React from "react"
import {
  Command as CommandPrimitive,
  CommandEmpty as CommandEmptyPrimitive,
  CommandGroup as CommandGroupPrimitive,
  CommandGroupLabel,
  CommandInput as CommandInputPrimitive,
  CommandItem as CommandItemPrimitive,
  CommandList as CommandListPrimitive,
  CommandSeparator as CommandSeparatorPrimitive,
} from "@ui-sdk/react/command"
import { CheckIcon, SearchIcon } from "lucide-react"

import { cn, cnState } from "@/lib/utils"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { InputGroup, InputGroupAddon } from "@/components/ui/input-group"

function Command({ className, ...props }: React.ComponentProps<typeof CommandPrimitive>) {
  return (
    <CommandPrimitive
      className={cnState(
        "flex size-full flex-col overflow-hidden rounded-xl! bg-popover p-1 text-popover-foreground",
        className
      )}
      {...props}
    />
  )
}

function CommandDialog({
  title = "Command Palette",
  description = "Search for a command to run...",
  children,
  className,
  showCloseButton = false,
  ...props
}: Omit<React.ComponentProps<typeof Dialog>, "children"> & {
  title?: string
  description?: string
  className?: string
  showCloseButton?: boolean
  children: React.ReactNode
}) {
  return (
    <Dialog {...props}>
      <DialogContent
        className={cn(
          // Opened from the keyboard many times a day, so it appears at once.
          "top-1/3 translate-y-0 overflow-hidden rounded-xl! p-0 data-open:animate-none data-ending-style:animate-none",
          className
        )}
        showCloseButton={showCloseButton}
      >
        <DialogHeader className="sr-only">
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
  )
}

function CommandInput({
  className,
  ...props
}: React.ComponentProps<typeof CommandInputPrimitive>) {
  return (
    <div data-slot="command-input-wrapper" className="p-1 pb-0">
      <InputGroup className="h-8! rounded-lg! border-input/30 bg-input/30 shadow-none! *:data-[slot=input-group-addon]:pl-2!">
        {/* The InputGroup draws the one focus ring (data-focus-within), so
            the input doesn't add a second one inside it. */}
        <CommandInputPrimitive
          data-focus-within=""
          aria-label={props["aria-label"] ?? props.placeholder}
          className={cnState(
            "w-full text-base outline-hidden md:text-sm disabled:cursor-not-allowed disabled:opacity-50",
            className
          )}
          {...props}
        />
        <InputGroupAddon>
          <SearchIcon aria-hidden="true" className="size-4 shrink-0 opacity-50" />
        </InputGroupAddon>
      </InputGroup>
    </div>
  )
}

function CommandList({ className, ...props }: React.ComponentProps<typeof CommandListPrimitive>) {
  return (
    <CommandListPrimitive
      className={cnState(
        "no-scrollbar max-h-72 scroll-py-1 overflow-x-hidden overflow-y-auto outline-none",
        className
      )}
      {...props}
    />
  )
}

function CommandEmpty({ className, ...props }: React.ComponentProps<typeof CommandEmptyPrimitive>) {
  return (
    <CommandEmptyPrimitive
      className={cnState("py-6 text-center text-sm not-data-empty:hidden", className)}
      {...props}
    />
  )
}

function CommandGroup({
  className,
  heading,
  children,
  ...props
}: Omit<React.ComponentProps<typeof CommandGroupPrimitive>, "children"> & {
  heading?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <CommandGroupPrimitive
      className={cnState("overflow-hidden p-1 text-foreground", className)}
      {...props}
    >
      {heading && (
        <CommandGroupLabel className="px-2 py-1.5 text-xs font-medium text-muted-foreground">
          {heading}
        </CommandGroupLabel>
      )}
      {children}
    </CommandGroupPrimitive>
  )
}

function CommandSeparator({
  className,
  ...props
}: React.ComponentProps<typeof CommandSeparatorPrimitive>) {
  return <CommandSeparatorPrimitive className={cnState("-mx-1 h-px bg-border", className)} {...props} />
}

// cmdk searched an item's value as well as its text; keywords keep that.
function CommandItem({
  className,
  children,
  value,
  keywords,
  ...props
}: Omit<React.ComponentProps<typeof CommandItemPrimitive>, "children"> & {
  children?: React.ReactNode
}) {
  return (
    <CommandItemPrimitive
      value={value}
      keywords={value ? [...(keywords ?? []), value] : keywords}
      className={cnState(
        "group/command-item relative flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none in-data-[slot=dialog-content]:rounded-lg! data-disabled:pointer-events-none data-disabled:opacity-50 data-highlighted:bg-muted data-highlighted:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 data-highlighted:*:[svg]:text-foreground",
        className
      )}
      {...props}
    >
      {children}
      <CheckIcon
        aria-hidden="true"
        className="ml-auto opacity-0 group-has-data-[slot=command-shortcut]/command-item:hidden group-data-[checked=true]/command-item:opacity-100"
      />
    </CommandItemPrimitive>
  )
}

function CommandShortcut({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="command-shortcut"
      className={cn(
        "ml-auto text-xs tracking-widest text-muted-foreground group-data-highlighted/command-item:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Command,
  CommandDialog,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandShortcut,
  CommandSeparator,
}
