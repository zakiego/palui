import * as React from "react"
import { Input as InputPrimitive } from "@ui-sdk/react/input"

import { cnState } from "@/lib/utils"

function Input({ className, ...props }: React.ComponentProps<typeof InputPrimitive>) {
  return (
    <InputPrimitive
      className={cnState(
        "h-8 w-full min-w-0 rounded-lg border border-input bg-background px-2.5 py-1 text-base transition-[border-color,background-color] duration-150 outline-none pointer-coarse:h-10 file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground hover:border-foreground/40 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive md:text-sm",
        className
      )}
      {...props}
    />
  )
}

export { Input }
