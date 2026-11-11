// A side Sheet on wide screens, a bottom Drawer under 768px (PLAN 11.5). Same
// API either way, so a page writes its panel once. The drawer drags to dismiss.
import * as React from "react"
import { Drawer, DrawerContent, DrawerDescription, DrawerFooter, DrawerHeader, DrawerTitle } from "@/components/ui/drawer"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { useIsMobile } from "@/hooks/use-mobile"
import { cn } from "@/lib/utils"

export function ResponsiveSheet({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  className,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description?: React.ReactNode
  children: React.ReactNode
  footer?: React.ReactNode
  className?: string
}) {
  const mobile = useIsMobile()
  // A sheet that mounts already open (its content arrived with the click)
  // still animates in: it renders closed for one commit, then opens.
  const [mounted, setMounted] = React.useState(false)
  React.useEffect(() => setMounted(true), [])
  open = open && mounted
  if (mobile) {
    return (
      <Drawer open={open} onOpenChange={onOpenChange} showSwipeHandle>
        <DrawerContent>
          <DrawerHeader className="group-data-[swipe-axis=y]/drawer-popup:text-start">
            <DrawerTitle className="min-w-0 break-words">{title}</DrawerTitle>
            {description && <DrawerDescription>{description}</DrawerDescription>}
          </DrawerHeader>
          <div className={cn("flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto overscroll-contain p-4", className)}>{children}</div>
          {footer && <DrawerFooter className="border-t pb-[max(1rem,env(safe-area-inset-bottom))]">{footer}</DrawerFooter>}
        </DrawerContent>
      </Drawer>
    )
  }
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full gap-0 sm:max-w-lg">
        <SheetHeader className="border-b pe-12">
          <SheetTitle className="min-w-0 break-words">{title}</SheetTitle>
          {description && <SheetDescription>{description}</SheetDescription>}
        </SheetHeader>
        <div className={cn("flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto overscroll-contain p-4", className)}>{children}</div>
        {footer && <SheetFooter className="border-t">{footer}</SheetFooter>}
      </SheetContent>
    </Sheet>
  )
}
