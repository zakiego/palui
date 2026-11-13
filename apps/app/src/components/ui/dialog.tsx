"use client"

import * as React from "react"
import {
  Dialog as DialogPrimitive,
  DialogBackdrop,
  DialogClose as DialogClosePrimitive,
  DialogContent as DialogContentPrimitive,
  DialogDescription as DialogDescriptionPrimitive,
  DialogTitle as DialogTitlePrimitive,
  DialogTrigger as DialogTriggerPrimitive,
} from "@ui-sdk/react/dialog"
import { XIcon } from "lucide-react"

import { cn, cnState } from "@/lib/utils"
import { Button, partRender } from "@/components/ui/button"

function Dialog(props: React.ComponentProps<typeof DialogPrimitive>) {
  return <DialogPrimitive {...props} />
}

function DialogTrigger({ render, ...props }: React.ComponentProps<typeof DialogTriggerPrimitive>) {
  return <DialogTriggerPrimitive render={partRender(render)} {...props} />
}

function DialogClose({ render, ...props }: React.ComponentProps<typeof DialogClosePrimitive>) {
  return <DialogClosePrimitive render={partRender(render)} {...props} />
}

function DialogOverlay({ className, ...props }: React.ComponentProps<typeof DialogBackdrop>) {
  return (
    <DialogBackdrop
      className={cnState(
        "fixed inset-0 isolate z-50 size-full bg-black/20 duration-150 dark:bg-black/50 data-open:animate-in data-open:fade-in-0 data-ending-style:animate-out data-ending-style:fade-out-0",
        className
      )}
      {...props}
    />
  )
}

function DialogContent({
  className,
  children,
  showCloseButton = true,
  ...props
}: Omit<React.ComponentProps<typeof DialogContentPrimitive>, "children"> & {
  children?: React.ReactNode
  showCloseButton?: boolean
}) {
  return (
    <>
      <DialogOverlay />
      <DialogContentPrimitive
        className={cnState(
          "fixed top-1/2 left-1/2 z-50 grid w-full text-start whitespace-normal max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-5 overscroll-contain rounded-2xl bg-popover p-5 text-sm text-popover-foreground shadow-(--shadow-elevated) duration-200 ease-emphasized outline-none data-ending-style:duration-150 sm:max-w-md data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-ending-style:animate-out data-ending-style:fade-out-0 data-ending-style:zoom-out-95",
          className
        )}
        {...props}
      >
        {children}
        {showCloseButton && (
          <DialogClose
            aria-label="Close"
            render={<Button variant="ghost" className="absolute top-3 end-3" size="icon-sm" />}
          >
            <XIcon aria-hidden="true" />
          </DialogClose>
        )}
      </DialogContentPrimitive>
    </>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-1.5", className)}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-5 -mb-5 flex flex-col-reverse gap-2 rounded-b-2xl border-t bg-muted/50 px-5 py-4 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogClose render={<Button variant="outline" />}>Close</DialogClose>
      )}
    </div>
  )
}

function DialogTitle({ className, ...props }: React.ComponentProps<typeof DialogTitlePrimitive>) {
  return (
    <DialogTitlePrimitive
      className={cnState("font-heading text-heading pe-8", className)}
      {...props}
    />
  )
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogDescriptionPrimitive>) {
  return (
    <DialogDescriptionPrimitive
      className={cnState(
        "text-sm text-pretty text-muted-foreground *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogTitle,
  DialogTrigger,
}
