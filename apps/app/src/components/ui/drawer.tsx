"use client"

import * as React from "react"
import {
  Drawer as DrawerPrimitive,
  DrawerBackdrop,
  DrawerContent as DrawerContentPrimitive,
  DrawerDescription as DrawerDescriptionPrimitive,
  DrawerTitle as DrawerTitlePrimitive,
} from "@ui-sdk/react/drawer"

import { cn, cnState } from "@/lib/utils"

const DrawerContext = React.createContext({ showSwipeHandle: false })

function Drawer({
  showSwipeHandle = false,
  side = "bottom",
  ...props
}: React.ComponentProps<typeof DrawerPrimitive> & {
  showSwipeHandle?: boolean
}) {
  const value = React.useMemo(() => ({ showSwipeHandle }), [showSwipeHandle])
  return (
    <DrawerContext.Provider value={value}>
      <DrawerPrimitive side={side} {...props} />
    </DrawerContext.Provider>
  )
}

function DrawerOverlay({ className, ...props }: React.ComponentProps<typeof DrawerBackdrop>) {
  return (
    <DrawerBackdrop
      className={cnState(
        "fixed inset-0 z-50 size-full min-h-dvh bg-black/10 select-none supports-backdrop-filter:backdrop-blur-xs",
        className
      )}
      {...props}
    />
  )
}

function DrawerSwipeHandle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="drawer-swipe-handle"
      aria-hidden="true"
      className={cn(
        "relative z-10 flex h-3 w-full shrink-0 cursor-grab items-end justify-center group-data-[side=top]/drawer-popup:order-last group-data-[side=top]/drawer-popup:items-start after:block after:h-1 after:w-24 after:shrink-0 after:rounded-full after:bg-muted active:cursor-grabbing",
        className
      )}
      {...props}
    />
  )
}

// The drawer moves and dims itself, so these classes only give the sheet its
// size, surface and corners.
function DrawerContent({
  className,
  children,
  ...props
}: Omit<React.ComponentProps<typeof DrawerContentPrimitive>, "children"> & {
  children?: React.ReactNode
}) {
  const { showSwipeHandle } = React.useContext(DrawerContext)
  return (
    <>
      <DrawerOverlay />
      <DrawerContentPrimitive
        className={cnState(
          cn(
            "group/drawer-popup flex min-h-0 flex-col text-start whitespace-normal overflow-hidden overscroll-contain bg-popover text-sm text-popover-foreground outline-none",
            "data-[side=bottom]:max-h-[calc(100dvh-6rem)] data-[side=bottom]:w-full data-[side=bottom]:rounded-t-xl data-[side=bottom]:border-t data-[side=top]:max-h-[calc(100dvh-6rem)] data-[side=top]:w-full data-[side=top]:rounded-b-xl data-[side=top]:border-b",
            "data-[side=left]:h-full data-[side=left]:w-3/4 data-[side=left]:rounded-r-xl data-[side=left]:border-r data-[side=right]:h-full data-[side=right]:w-3/4 data-[side=right]:rounded-l-xl data-[side=right]:border-l sm:data-[side=left]:w-96 sm:data-[side=right]:w-96"
          ),
          className
        )}
        {...props}
      >
        {showSwipeHandle && <DrawerSwipeHandle />}
        {children}
      </DrawerContentPrimitive>
    </>
  )
}

function DrawerHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="drawer-header"
      className={cn(
        "flex shrink-0 flex-col gap-0.5 p-4 pb-0 group-data-[side=bottom]/drawer-popup:text-center md:gap-0.5 md:text-left",
        className
      )}
      {...props}
    />
  )
}

function DrawerFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="drawer-footer"
      className={cn("mt-auto flex shrink-0 flex-col gap-2 p-4 pt-0", className)}
      {...props}
    />
  )
}

function DrawerTitle({ className, ...props }: React.ComponentProps<typeof DrawerTitlePrimitive>) {
  return (
    <DrawerTitlePrimitive
      className={cnState("font-heading text-base font-medium text-foreground", className)}
      {...props}
    />
  )
}

function DrawerDescription({
  className,
  ...props
}: React.ComponentProps<typeof DrawerDescriptionPrimitive>) {
  return (
    <DrawerDescriptionPrimitive
      className={cnState("text-sm text-balance text-muted-foreground", className)}
      {...props}
    />
  )
}

export {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerFooter,
  DrawerTitle,
  DrawerDescription,
}
