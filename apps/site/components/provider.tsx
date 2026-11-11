"use client"

import { RootProvider } from "fumadocs-ui/provider/next"
import type { ReactNode } from "react"
import SearchDialog from "@/components/search"

// Dark by default, like the landing. Readers can still switch the docs to
// light, and the choice sticks.
export function Provider({ children }: { children: ReactNode }) {
  return (
    <RootProvider theme={{ defaultTheme: "dark" }} search={{ SearchDialog }}>
      {children}
    </RootProvider>
  )
}
