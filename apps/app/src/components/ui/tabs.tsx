import * as React from "react"
import {
  Tabs as TabsPrimitive,
  TabsList as TabsListPrimitive,
  TabsPanel,
  TabsTab,
} from "@ui-sdk/react/tabs"
import { cva, type VariantProps } from "class-variance-authority"

import { cn, cnState } from "@/lib/utils"

function Tabs({ className, ...props }: React.ComponentProps<typeof TabsPrimitive>) {
  return (
    <TabsPrimitive
      className={cnState(
        "group/tabs flex gap-2 data-horizontal:flex-col",
        className
      )}
      {...props}
    />
  )
}

const tabsListVariants = cva(
  "group/tabs-list inline-flex w-fit items-center justify-center rounded-lg p-[3px] text-muted-foreground group-data-horizontal/tabs:h-8 pointer-coarse:group-data-horizontal/tabs:h-10 group-data-vertical/tabs:h-fit group-data-vertical/tabs:flex-col data-[variant=line]:rounded-none",
  {
    variants: {
      variant: {
        default: "bg-muted",
        // Geist-style page tabs: a full-width hairline with plain labels on it.
        // The list sits 6px wider on each side so the triggers' padding (their
        // hover and focus box) doesn't push the first label off the content
        // edge, and the hairline is a background so it stays inside that edge.
        // Pages hide tab icons below @xl/main so five labels fit at 390px.
        // Narrower than that it scrolls sideways (see useLineOverflow).
        line: "-mx-1.5 w-[calc(100%+0.75rem)] justify-start gap-0 overflow-x-auto overflow-y-hidden bg-transparent bg-[linear-gradient(var(--border),var(--border))] bg-[length:calc(100%-0.75rem)_1px] bg-[position:0.375rem_100%] bg-no-repeat p-0 [scrollbar-width:none] group-data-horizontal/tabs:h-auto pointer-coarse:group-data-horizontal/tabs:h-auto @xl/main:gap-4 [&::-webkit-scrollbar]:hidden",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

// A line list that doesn't fit scrolls with its scrollbar hidden, so the
// clipped edge fades out to show there's more, and the active tab is scrolled
// into view on load and whenever it changes (a deep link to the last tab).
function useLineOverflow(enabled: boolean) {
  const ref = React.useRef<HTMLDivElement>(null)
  React.useEffect(() => {
    const list = ref.current
    if (!enabled || !list) return
    const fade = "2rem"
    const update = () => {
      const max = list.scrollWidth - list.clientWidth
      const start = list.scrollLeft > 1
      const end = list.scrollLeft < max - 1
      list.style.maskImage =
        start || end
          ? `linear-gradient(to right, ${start ? "transparent" : "black"}, black ${start ? fade : "0%"}, black ${end ? `calc(100% - ${fade})` : "100%"}, ${end ? "transparent" : "black"})`
          : ""
    }
    const reveal = () => {
      const tab = list.querySelector<HTMLElement>("[data-selected]")
      if (!tab) return
      const box = list.getBoundingClientRect()
      const r = tab.getBoundingClientRect()
      if (r.left < box.left) list.scrollLeft += r.left - box.left - 24
      else if (r.right > box.right) list.scrollLeft += r.right - box.right + 24
      update()
    }
    reveal()
    update()
    list.addEventListener("scroll", update, { passive: true })
    const resize = new ResizeObserver(update)
    resize.observe(list)
    const active = new MutationObserver(reveal)
    active.observe(list, { subtree: true, attributes: true, attributeFilter: ["data-selected"] })
    return () => {
      list.removeEventListener("scroll", update)
      resize.disconnect()
      active.disconnect()
    }
  }, [enabled])
  return ref
}

function TabsList({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<typeof TabsListPrimitive> & VariantProps<typeof tabsListVariants>) {
  const ref = useLineOverflow(variant === "line")
  return (
    <TabsListPrimitive
      ref={ref}
      data-variant={variant}
      className={cnState(tabsListVariants({ variant }), className)}
      {...props}
    />
  )
}

// Default: a segmented control. The list is `rounded-lg` with a 3px inset, so
// the trigger takes 10px minus 3px and the two curves stay concentric.
// Line: a 40px label on the hairline, a 2px foreground bar under the active
// one (as wide as its label, a border so it survives forced colors), and
// color only on hover. Nothing slides. The global focus outline is pulled
// inside the trigger, since the scrolling list would clip it.
function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsTab>) {
  return (
    <TabsTab
      className={cnState(
        cn(
        "relative inline-flex h-full flex-1 cursor-pointer items-center justify-center gap-1.5 rounded-[calc(var(--radius-lg)-3px)] px-2.5 text-[0.8125rem] font-medium whitespace-nowrap text-muted-foreground transition-[background-color,color,box-shadow] duration-150 outline-none hover:duration-0 group-data-vertical/tabs:w-full group-data-vertical/tabs:justify-start hover:text-foreground disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        "data-selected:bg-card data-selected:text-foreground data-selected:shadow-(--shadow-border)",
        "group-data-[variant=line]/tabs-list:h-10 group-data-[variant=line]/tabs-list:flex-none group-data-[variant=line]/tabs-list:rounded-md group-data-[variant=line]/tabs-list:px-1.5 group-data-[variant=line]/tabs-list:text-sm group-data-[variant=line]/tabs-list:focus-visible:-outline-offset-2! group-data-[variant=line]/tabs-list:data-selected:bg-transparent group-data-[variant=line]/tabs-list:data-selected:shadow-none",
        "group-data-[variant=line]/tabs-list:after:absolute group-data-[variant=line]/tabs-list:after:inset-x-1.5 group-data-[variant=line]/tabs-list:after:bottom-0 group-data-[variant=line]/tabs-list:after:border-b-2 group-data-[variant=line]/tabs-list:after:border-foreground group-data-[variant=line]/tabs-list:after:opacity-0 group-data-[variant=line]/tabs-list:data-selected:after:opacity-100"
        ),
        className
      )}
      {...props}
    />
  )
}

function TabsContent({ className, ...props }: React.ComponentProps<typeof TabsPanel>) {
  return (
    <TabsPanel
      className={cnState("flex-1 text-sm", className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent, tabsListVariants }
