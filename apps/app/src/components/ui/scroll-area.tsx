import * as React from "react"
import {
  ScrollArea as ScrollAreaPrimitive,
  ScrollAreaCorner,
  ScrollAreaScrollbar,
  ScrollAreaThumb,
  ScrollAreaViewport,
} from "@ui-sdk/react/scroll-area"

import { cnState } from "@/lib/utils"

function ScrollArea({
  className,
  children,
  type = "auto",
  ...props
}: Omit<React.ComponentProps<typeof ScrollAreaPrimitive>, "children"> & {
  children?: React.ReactNode
}) {
  return (
    <ScrollAreaPrimitive type={type} className={cnState("relative", className)} {...props}>
      <ScrollAreaViewport className="size-full rounded-[inherit] transition-[color,box-shadow] outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1">
        {children}
      </ScrollAreaViewport>
      <ScrollBar />
      <ScrollAreaCorner />
    </ScrollAreaPrimitive>
  )
}

function ScrollBar({
  className,
  orientation = "vertical",
  ...props
}: React.ComponentProps<typeof ScrollAreaScrollbar>) {
  return (
    <ScrollAreaScrollbar
      orientation={orientation}
      className={cnState(
        "flex touch-none p-px opacity-0 transition-[opacity,color] select-none data-visible:opacity-100 data-horizontal:h-2.5 data-horizontal:flex-col data-horizontal:border-t data-horizontal:border-t-transparent data-vertical:h-full data-vertical:w-2.5 data-vertical:border-l data-vertical:border-l-transparent",
        className
      )}
      {...props}
    >
      <ScrollAreaThumb className="relative flex-1 rounded-full bg-border" />
    </ScrollAreaScrollbar>
  )
}

export { ScrollArea, ScrollBar }
