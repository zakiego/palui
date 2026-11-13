"use client"

import * as React from "react"
import { Toggle, ToggleGroup as ToggleGroupPrimitive } from "@ui-sdk/react/toggle"
import { type VariantProps } from "class-variance-authority"

import { cn, cnState } from "@/lib/utils"
import { toggleVariants } from "@/components/ui/toggle"

const ToggleGroupContext = React.createContext<
  VariantProps<typeof toggleVariants> & { spacing?: number }
>({
  size: "default",
  variant: "default",
  spacing: 2,
})

function ToggleGroup({
  className,
  variant,
  size,
  spacing = 2,
  children,
  ...props
}: Omit<React.ComponentProps<typeof ToggleGroupPrimitive>, "children"> &
  VariantProps<typeof toggleVariants> & {
    spacing?: number
    children?: React.ReactNode
  }) {
  return (
    <ToggleGroupPrimitive
      data-variant={variant}
      data-size={size}
      data-spacing={spacing}
      style={{ "--gap": spacing } as React.CSSProperties}
      className={cnState(
        "group/toggle-group flex w-fit flex-row items-center gap-[--spacing(var(--gap))] rounded-lg data-[size=sm]:rounded-[min(var(--radius-md),10px)] data-vertical:flex-col data-vertical:items-stretch",
        className
      )}
      {...props}
    >
      <ToggleGroupContext.Provider value={{ variant, size, spacing }}>
        {children}
      </ToggleGroupContext.Provider>
    </ToggleGroupPrimitive>
  )
}

function ToggleGroupItem({
  className,
  variant = "default",
  size = "default",
  ...props
}: React.ComponentProps<typeof Toggle> & VariantProps<typeof toggleVariants>) {
  const context = React.useContext(ToggleGroupContext)

  return (
    <Toggle
      data-variant={context.variant || variant}
      data-size={context.size || size}
      data-spacing={context.spacing}
      className={cnState(
        cn(
          "shrink-0 group-data-[spacing=0]/toggle-group:rounded-none group-data-[spacing=0]/toggle-group:px-2 focus:z-10 focus-visible:z-10 group-data-[spacing=0]/toggle-group:has-data-[icon=inline-end]:pr-1.5 group-data-[spacing=0]/toggle-group:has-data-[icon=inline-start]:pl-1.5 group-data-horizontal/toggle-group:data-[spacing=0]:first:rounded-l-lg group-data-vertical/toggle-group:data-[spacing=0]:first:rounded-t-lg group-data-horizontal/toggle-group:data-[spacing=0]:last:rounded-r-lg group-data-vertical/toggle-group:data-[spacing=0]:last:rounded-b-lg group-data-horizontal/toggle-group:data-[spacing=0]:data-[variant=outline]:border-l-0 group-data-vertical/toggle-group:data-[spacing=0]:data-[variant=outline]:border-t-0 group-data-horizontal/toggle-group:data-[spacing=0]:data-[variant=outline]:first:border-l group-data-vertical/toggle-group:data-[spacing=0]:data-[variant=outline]:first:border-t",
          toggleVariants({
            variant: context.variant || variant,
            size: context.size || size,
          })
        ),
        className
      )}
      {...props}
    />
  )
}

export { ToggleGroup, ToggleGroupItem }
