"use client"

import * as React from "react"
import {
  ScrollArea,
  ScrollAreaContent,
  ScrollAreaScrollToEnd,
  ScrollAreaViewport,
} from "@ui-sdk/react/scroll-area"
import { ArrowDownIcon } from "lucide-react"

import { cn, cnState } from "@/lib/utils"
import { Button, partRender } from "@/components/ui/button"

function MessageScroller({
  className,
  stickToEnd = true,
  ...props
}: React.ComponentProps<typeof ScrollArea>) {
  return (
    <ScrollArea
      stickToEnd={stickToEnd}
      className={cnState(
        "group/message-scroller relative flex size-full min-h-0 flex-col overflow-hidden",
        className
      )}
      {...props}
    />
  )
}

function MessageScrollerViewport({
  className,
  ...props
}: React.ComponentProps<typeof ScrollAreaViewport>) {
  return (
    <ScrollAreaViewport
      className={cnState(
        "size-full min-h-0 min-w-0 scroll-fade-b overflow-y-auto overscroll-contain contain-content [scrollbar-gutter:stable] [scrollbar-width:thin]",
        className
      )}
      {...props}
    />
  )
}

function MessageScrollerContent({
  className,
  ...props
}: React.ComponentProps<typeof ScrollAreaContent>) {
  return (
    <ScrollAreaContent
      className={cnState("flex h-max min-h-full flex-col gap-6", className)}
      {...props}
    />
  )
}

function MessageScrollerItem({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="message-scroller-item"
      className={cn(
        "min-w-0 shrink-0 [contain-intrinsic-size:auto_10rem] [content-visibility:auto]",
        className
      )}
      {...props}
    />
  )
}

function MessageScrollerButton({
  className,
  children,
  variant = "secondary",
  size = "icon-sm",
  ...props
}: React.ComponentProps<typeof ScrollAreaScrollToEnd> &
  Pick<React.ComponentProps<typeof Button>, "variant" | "size">) {
  return (
    <ScrollAreaScrollToEnd
      aria-label="Scroll to end"
      className={cnState(
        "absolute inset-s-1/2 bottom-4 -translate-x-1/2 border-border bg-background text-foreground transition-[translate,scale,opacity] duration-200 hover:bg-muted hover:text-foreground not-data-visible:pointer-events-none not-data-visible:translate-y-full not-data-visible:scale-95 not-data-visible:opacity-0 not-data-visible:duration-400 not-data-visible:ease-[cubic-bezier(0.7,0,0.84,0)] data-visible:translate-y-0 data-visible:scale-100 data-visible:opacity-100 data-visible:ease-[cubic-bezier(0.23,1,0.32,1)] rtl:translate-x-1/2",
        className
      )}
      render={partRender(<Button variant={variant} size={size} />)}
      {...props}
    >
      {children ?? <ArrowDownIcon aria-hidden="true" />}
    </ScrollAreaScrollToEnd>
  )
}

export {
  MessageScroller,
  MessageScrollerViewport,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerButton,
}
