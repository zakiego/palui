import * as React from "react"
import { Textarea as TextareaPrimitive } from "@ui-sdk/react/textarea"

import { cnState } from "@/lib/utils"

function Textarea({ className, ...props }: React.ComponentProps<typeof TextareaPrimitive>) {
  return (
    <TextareaPrimitive
      className={cnState(
        "flex field-sizing-content min-h-16 w-full rounded-lg border border-input bg-background px-2.5 py-2 text-base transition-[border-color,background-color] duration-150 outline-none placeholder:text-muted-foreground hover:border-foreground/40 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive md:text-sm",
        className
      )}
      {...props}
    />
  )
}

export { Textarea }
