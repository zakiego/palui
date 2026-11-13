import { clsx, type ClassValue } from "clsx"
import { extendTailwindMerge } from "tailwind-merge"

/**
 * tailwind-merge does not know the type roles defined in index.css, so it
 * would read `text-caption` as a color and drop it next to
 * `text-muted-foreground`. Registering them as font sizes keeps both.
 */
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: {
      "font-size": [
        { text: ["display", "title", "heading", "caption", "overline"] },
      ],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Adapt a select handler to one that only sees a value.
 *
 * ui-sdk reports `null` when a select is cleared. Every select here is a
 * required choice, with "all" and "none" spelled as sentinel values of their
 * own, so a null is not a state any of them can be in.
 */
export function picked(fn: (value: string) => void) {
  return (value: string | null) => {
    if (value !== null) fn(value)
  }
}

type StateClassName<S> = string | ((state: S) => string | undefined) | undefined

// ui-sdk parts also take `className` as a function of their state.
export function cnState<S>(own: string, className: StateClassName<S>) {
  if (typeof className === "function") {
    return (state: S) => cn(own, className(state))
  }
  return cn(own, className)
}
