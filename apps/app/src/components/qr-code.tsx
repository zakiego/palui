// A QR code drawn in the browser from a string. Generated locally, so no SVG
// from the network is ever injected into the page.
import * as React from "react"
import QRCode from "qrcode"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

export function QrCode({ value, label, className }: { value: string; label: string; className?: string }) {
  const [svg, setSvg] = React.useState<string>()
  React.useEffect(() => {
    let alive = true
    QRCode.toString(value, { type: "svg", margin: 1, errorCorrectionLevel: "M", color: { dark: "#000000", light: "#ffffff" } })
      .then((s) => alive && setSvg(s))
      .catch(() => alive && setSvg(undefined))
    return () => {
      alive = false
    }
  }, [value])
  if (!svg) return <Skeleton className={cn("aspect-square w-48 rounded-lg", className)} />
  return (
    <div
      role="img"
      aria-label={label}
      className={cn("aspect-square w-48 overflow-hidden rounded-lg bg-white p-2 shadow-(--shadow-border) [&_svg]:size-full", className)}
      // biome-ignore lint: the SVG is generated locally by the qrcode package.
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  )
}
