"use client"

import * as React from "react"
import {
  Dialog as SheetPrimitive,
  DialogBackdrop,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@ui-sdk/react/dialog"
import { XIcon } from "lucide-react"

import { cn, cnState } from "@/lib/utils"
import { Button, partRender } from "@/components/ui/button"

function Sheet(props: React.ComponentProps<typeof SheetPrimitive>) {
  return <SheetPrimitive {...props} />
}

function SheetTrigger({ render, ...props }: React.ComponentProps<typeof DialogTrigger>) {
  return <DialogTrigger render={partRender(render)} {...props} />
}

function SheetClose({ render, ...props }: React.ComponentProps<typeof DialogClose>) {
  return <DialogClose render={partRender(render)} {...props} />
}

function SheetOverlay({ className, ...props }: React.ComponentProps<typeof DialogBackdrop>) {
  return (
    <DialogBackdrop
      className={cnState(
        "fixed inset-0 z-50 size-full bg-black/20 transition-opacity duration-300 ease-drawer data-ending-style:opacity-0 data-ending-style:duration-200 data-starting-style:opacity-0 dark:bg-black/50",
        className
      )}
      {...props}
    />
  )
}

function SheetContent({
  className,
  children,
  side = "right",
  showCloseButton = true,
  ...props
}: Omit<React.ComponentProps<typeof DialogContent>, "children"> & {
  children?: React.ReactNode
  side?: "top" | "right" | "bottom" | "left"
  showCloseButton?: boolean
}) {
  return (
    <>
      <SheetOverlay />
      <DialogContent
        data-side={side}
        className={cnState(
          "fixed z-50 flex max-h-none max-w-none flex-col text-start whitespace-normal gap-4 overscroll-contain bg-card bg-clip-padding text-sm text-card-foreground shadow-(--shadow-elevated) transition-[opacity,translate] duration-300 ease-drawer data-ending-style:opacity-0 data-ending-style:duration-200 data-starting-style:opacity-0 data-[side=bottom]:inset-x-0 data-[side=bottom]:top-auto data-[side=bottom]:bottom-0 data-[side=bottom]:h-auto data-[side=bottom]:w-full data-[side=bottom]:border-t data-[side=bottom]:data-ending-style:translate-y-[2.5rem] data-[side=bottom]:data-starting-style:translate-y-[2.5rem] data-[side=left]:inset-y-0 data-[side=left]:right-auto data-[side=left]:left-0 data-[side=left]:h-full data-[side=left]:w-3/4 data-[side=left]:data-ending-style:translate-x-[-2.5rem] data-[side=left]:data-starting-style:translate-x-[-2.5rem] data-[side=right]:inset-y-0 data-[side=right]:right-0 data-[side=right]:left-auto data-[side=right]:h-full data-[side=right]:w-3/4 data-[side=right]:data-ending-style:translate-x-[2.5rem] data-[side=right]:data-starting-style:translate-x-[2.5rem] data-[side=top]:inset-x-0 data-[side=top]:top-0 data-[side=top]:bottom-auto data-[side=top]:h-auto data-[side=top]:w-full data-[side=top]:border-b data-[side=top]:data-ending-style:translate-y-[-2.5rem] data-[side=top]:data-starting-style:translate-y-[-2.5rem] sm:max-w-sm",
          className
        )}
        {...props}
      >
        {children}
        {showCloseButton && (
          <SheetClose
            aria-label="Close"
            render={<Button variant="ghost" className="absolute top-3.5 end-3.5" size="icon-sm" />}
          >
            <XIcon aria-hidden="true" />
          </SheetClose>
        )}
      </DialogContent>
    </>
  )
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-header"
      className={cn("flex flex-col gap-1 border-b px-5 py-4 pe-14", className)}
      {...props}
    />
  )
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-footer"
      className={cn("mt-auto flex flex-col gap-3 px-5 py-4", className)}
      {...props}
    />
  )
}

function SheetTitle({ className, ...props }: React.ComponentProps<typeof DialogTitle>) {
  return (
    <DialogTitle
      className={cnState("font-heading text-heading text-foreground", className)}
      {...props}
    />
  )
}

function SheetDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogDescription>) {
  return (
    <DialogDescription
      className={cnState("text-sm text-pretty text-muted-foreground", className)}
      {...props}
    />
  )
}

export {
  Sheet,
  SheetTrigger,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetFooter,
  SheetTitle,
  SheetDescription,
}
