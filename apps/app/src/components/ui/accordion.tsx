import * as React from "react"
import {
  Accordion as AccordionPrimitive,
  AccordionHeader,
  AccordionItem as AccordionItemPrimitive,
  AccordionPanel,
  AccordionTrigger as AccordionTriggerPrimitive,
} from "@ui-sdk/react/accordion"
import { ChevronDownIcon } from "lucide-react"

import { cn, cnState } from "@/lib/utils"

function Accordion({ className, ...props }: React.ComponentProps<typeof AccordionPrimitive>) {
  return <AccordionPrimitive className={cnState("flex w-full flex-col", className)} {...props} />
}

function AccordionItem({ className, ...props }: React.ComponentProps<typeof AccordionItemPrimitive>) {
  return <AccordionItemPrimitive className={cnState("not-last:border-b", className)} {...props} />
}

// One chevron that turns, like the Rows chevron (DESIGN.md Motion): a CSS
// transition, so a quick second tap reverses it from where it is.
function AccordionTrigger({
  className,
  children,
  ...props
}: Omit<React.ComponentProps<typeof AccordionTriggerPrimitive>, "children"> & {
  children?: React.ReactNode
}) {
  return (
    <AccordionHeader className="flex">
      <AccordionTriggerPrimitive
        className={cnState(
          "group/accordion-trigger relative flex flex-1 items-start justify-between rounded-lg border border-transparent py-2.5 text-start text-sm font-medium transition-[color,box-shadow] outline-none hover:underline focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:after:border-ring aria-disabled:pointer-events-none aria-disabled:opacity-50 **:data-[slot=accordion-trigger-icon]:ml-auto **:data-[slot=accordion-trigger-icon]:size-4 **:data-[slot=accordion-trigger-icon]:text-muted-foreground",
          className,
        )}
        {...props}
      >
        {children}
        <ChevronDownIcon
          data-slot="accordion-trigger-icon"
          aria-hidden="true"
          className="pointer-events-none shrink-0 transition-transform duration-150 ease-emphasized group-aria-expanded/accordion-trigger:rotate-180"
        />
      </AccordionTriggerPrimitive>
    </AccordionHeader>
  )
}

// Height is a transition, not keyframes, so closing mid-open turns around
// smoothly. Closing is quicker than opening. Reduced motion makes it
// instant (the global rule in app.css).
function AccordionContent({
  className,
  children,
  ...props
}: Omit<React.ComponentProps<typeof AccordionPanel>, "className" | "children"> & {
  className?: string
  children?: React.ReactNode
}) {
  return (
    <AccordionPanel
      className="h-(--accordion-panel-height) overflow-hidden text-sm transition-[height] duration-200 ease-emphasized data-ending-style:h-0 data-ending-style:duration-150 data-starting-style:h-0"
      {...props}
    >
      <div className={cn("pt-0 pb-2.5", className)}>{children}</div>
    </AccordionPanel>
  )
}

export { Accordion, AccordionItem, AccordionTrigger, AccordionContent }
