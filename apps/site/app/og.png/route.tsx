import { ogImage } from "@/lib/og"

// Exported as out/og.png, so Pages serves it with an image/png content type.
export const dynamic = "force-static"

export function GET() {
  return ogImage({
    kicker: "by Zaki",
    mascot: true,
    title: "An agent friend\nyou host yourself",
    description: "Palui remembers what you tell it, and every tool it uses is a switch you can flip without a restart.",
  })
}
