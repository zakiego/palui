"use client"

import * as React from "react"
import {
  Popover as PopoverPrimitive,
  PopoverContent as PopoverContentPrimitive,
  PopoverDescription as PopoverDescriptionPrimitive,
  PopoverTitle as PopoverTitlePrimitive,
  PopoverTrigger as PopoverTriggerPrimitive,
} from "@ui-sdk/react/popover"

import { partRender } from "@/components/ui/button"
import { cn, cnState } from "@/lib/utils"

function Popover(props: React.ComponentProps<typeof PopoverPrimitive>) {
  return <PopoverPrimitive {...props} />
}

function PopoverTrigger({ render, ...props }: React.ComponentProps<typeof PopoverTriggerPrimitive>) {
  return <PopoverTriggerPrimitive render={partRender(render)} {...props} />
}

function PopoverContent({
  className,
  align = "center",
  alignOffset = 0,
  side = "bottom",
  sideOffset = 4,
  ...props
}: React.ComponentProps<typeof PopoverContentPrimitive>) {
  return (
    <PopoverContentPrimitive
      align={align}
      alignOffset={alignOffset}
      side={side}
      sideOffset={sideOffset}
      className={cnState(
        "z-50 flex w-72 origin-(--transform-origin) flex-col text-start whitespace-normal gap-2.5 rounded-lg bg-popover p-2.5 text-sm text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-hidden duration-100 data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-ending-style:animate-out data-ending-style:fade-out-0 data-ending-style:zoom-out-95",
        className
      )}
      {...props}
    />
  )
}

function PopoverHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="popover-header"
      className={cn("flex flex-col gap-0.5 text-sm", className)}
      {...props}
    />
  )
}

function PopoverTitle({
  className,
  ...props
}: React.ComponentProps<typeof PopoverTitlePrimitive>) {
  return <PopoverTitlePrimitive className={cnState("font-medium", className)} {...props} />
}

function PopoverDescription({
  className,
  ...props
}: React.ComponentProps<typeof PopoverDescriptionPrimitive>) {
  return (
    <PopoverDescriptionPrimitive
      className={cnState("text-muted-foreground", className)}
      {...props}
    />
  )
}

export {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
}
