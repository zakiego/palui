"use client"

import * as React from "react"
import {
  Tooltip as TooltipPrimitive,
  TooltipArrow,
  TooltipContent as TooltipContentPrimitive,
  TooltipTrigger as TooltipTriggerPrimitive,
} from "@ui-sdk/react/tooltip"

import { partRender } from "@/components/ui/button"
import { cn, cnState } from "@/lib/utils"

function Tooltip({ delay = 500, ...props }: React.ComponentProps<typeof TooltipPrimitive>) {
  return <TooltipPrimitive delay={delay} {...props} />
}

function TooltipTrigger({ render, ...props }: React.ComponentProps<typeof TooltipTriggerPrimitive>) {
  return <TooltipTriggerPrimitive render={partRender(render)} {...props} />
}

function TooltipContent({
  className,
  side = "top",
  sideOffset = 4,
  align = "center",
  alignOffset = 0,
  children,
  ...props
}: Omit<React.ComponentProps<typeof TooltipContentPrimitive>, "children"> & {
  children?: React.ReactNode
}) {
  return (
    <TooltipContentPrimitive
      side={side}
      sideOffset={sideOffset}
      align={align}
      alignOffset={alignOffset}
      className={cnState(
        "group/tooltip z-50 inline-flex w-fit max-w-xs text-start whitespace-normal origin-(--transform-origin) items-center gap-1.5 overflow-visible rounded-md bg-foreground px-3 py-1.5 text-xs text-background has-data-[slot=kbd]:pr-1.5 data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 **:data-[slot=kbd]:relative **:data-[slot=kbd]:isolate **:data-[slot=kbd]:z-50 **:data-[slot=kbd]:rounded-sm data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-ending-style:animate-out data-ending-style:fade-out-0 data-ending-style:zoom-out-95 duration-150 ease-emphasized data-instant:animate-none!",
        className
      )}
      {...props}
    >
      {children}
      <TooltipArrow
        className={cn(
          "pointer-events-none absolute z-50 size-2.5 rotate-45 rounded-[2px] bg-foreground",
          "group-data-[side=top]/tooltip:-bottom-[3px] group-data-[side=bottom]/tooltip:-top-[3px] group-data-[side=top]/tooltip:left-(--arrow-offset) group-data-[side=bottom]/tooltip:left-(--arrow-offset) group-data-[side=top]/tooltip:-translate-x-1/2 group-data-[side=bottom]/tooltip:-translate-x-1/2",
          "group-data-[side=left]/tooltip:-right-1 group-data-[side=right]/tooltip:-left-1 group-data-[side=left]/tooltip:top-(--arrow-offset) group-data-[side=right]/tooltip:top-(--arrow-offset) group-data-[side=left]/tooltip:-translate-y-1/2 group-data-[side=right]/tooltip:-translate-y-1/2"
        )}
      />
    </TooltipContentPrimitive>
  )
}

export { Tooltip, TooltipTrigger, TooltipContent }
