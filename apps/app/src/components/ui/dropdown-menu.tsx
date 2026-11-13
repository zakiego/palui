"use client"

import * as React from "react"
import {
  Menu,
  MenuContent,
  MenuGroup,
  MenuGroupLabel,
  MenuItem,
  MenuSeparator,
  MenuTrigger,
} from "@ui-sdk/react/menu"

import { partRender } from "@/components/ui/button"
import { cnState } from "@/lib/utils"

function DropdownMenu(props: React.ComponentProps<typeof Menu>) {
  return <Menu {...props} />
}

function DropdownMenuTrigger({ render, ...props }: React.ComponentProps<typeof MenuTrigger>) {
  return <MenuTrigger render={partRender(render)} {...props} />
}

function DropdownMenuContent({
  align = "start",
  alignOffset = 0,
  side = "bottom",
  sideOffset = 4,
  className,
  ...props
}: React.ComponentProps<typeof MenuContent>) {
  return (
    <MenuContent
      align={align}
      alignOffset={alignOffset}
      side={side}
      sideOffset={sideOffset}
      className={cnState(
        "z-50 max-h-(--available-height) w-(--anchor-width) min-w-32 text-start whitespace-normal origin-(--transform-origin) overflow-x-hidden overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 duration-100 outline-none data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-ending-style:animate-out data-ending-style:overflow-hidden data-ending-style:fade-out-0 data-ending-style:zoom-out-95",
        className
      )}
      {...props}
    />
  )
}

function DropdownMenuGroup(props: React.ComponentProps<typeof MenuGroup>) {
  return <MenuGroup {...props} />
}

function DropdownMenuLabel({
  className,
  inset,
  ...props
}: React.ComponentProps<typeof MenuGroupLabel> & {
  inset?: boolean
}) {
  return (
    <MenuGroupLabel
      data-inset={inset}
      className={cnState(
        "px-1.5 py-1 text-xs font-medium text-muted-foreground data-inset:pl-7",
        className
      )}
      {...props}
    />
  )
}

function DropdownMenuItem({
  className,
  inset,
  variant = "default",
  ...props
}: React.ComponentProps<typeof MenuItem> & {
  inset?: boolean
  variant?: "default" | "destructive"
}) {
  return (
    <MenuItem
      data-inset={inset}
      data-variant={variant}
      destructive={variant === "destructive"}
      className={cnState(
        "group/dropdown-menu-item relative flex cursor-default items-center gap-1.5 rounded-md px-1.5 py-1 text-sm outline-hidden select-none data-highlighted:bg-accent data-highlighted:text-accent-foreground not-data-[variant=destructive]:data-highlighted:**:text-accent-foreground data-inset:pl-7 data-[variant=destructive]:text-destructive data-[variant=destructive]:data-highlighted:bg-destructive/10 data-[variant=destructive]:data-highlighted:text-destructive dark:data-[variant=destructive]:data-highlighted:bg-destructive/20 data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 data-[variant=destructive]:*:[svg]:text-destructive",
        className
      )}
      {...props}
    />
  )
}

function DropdownMenuSeparator({
  className,
  ...props
}: React.ComponentProps<typeof MenuSeparator>) {
  return <MenuSeparator className={cnState("-mx-1 my-1 h-px bg-border", className)} {...props} />
}

export {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuItem,
  DropdownMenuSeparator,
}
