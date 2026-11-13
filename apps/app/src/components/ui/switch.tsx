"use client"

import * as React from "react"
import { Switch as SwitchPrimitive, SwitchThumb } from "@ui-sdk/react/switch"

import { cnState } from "@/lib/utils"

function Switch({
  className,
  size = "default",
  ...props
}: React.ComponentProps<typeof SwitchPrimitive> & {
  size?: "sm" | "default"
}) {
  return (
    <SwitchPrimitive
      data-size={size}
      className={cnState(
        "peer group/switch relative inline-flex shrink-0 items-center rounded-full border border-transparent px-[3px] transition-[background-color] duration-150 outline-none group-has-[:focus-visible]/field-label:border-transparent group-has-[:focus-visible]/field-label:ring-0 after:absolute after:-inset-x-3 after:-inset-y-2 cursor-pointer aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 data-[size=default]:h-5 data-[size=default]:w-8 data-[size=sm]:h-4 data-[size=sm]:w-7 data-[size=sm]:px-0.5 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40 data-checked:bg-brand data-unchecked:bg-input data-disabled:cursor-not-allowed data-disabled:opacity-50",
        className
      )}
      {...props}
    >
      {/* Linear's thumb: white in both states, sliding on a long settle. */}
      <SwitchThumb className="pointer-events-none block rounded-full bg-white shadow-[0_1px_3px_rgb(0_0_0/0.25)] ring-0 transition-transform duration-200 ease-[cubic-bezier(0.16,1,0.3,1)] group-data-[size=default]/switch:size-3.5 group-data-[size=sm]/switch:size-3 data-checked:translate-x-2.5 rtl:data-checked:-translate-x-2.5 data-unchecked:translate-x-0" />
    </SwitchPrimitive>
  )
}

export { Switch }
