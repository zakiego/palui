// The centered frame for the pages outside the shell: login and the owner invite.
import type * as React from "react"
import { PaluiMark } from "@/components/palui-mark"
import { ThemeToggle } from "@/components/theme-toggle"

export function AuthFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="relative flex min-h-svh flex-col items-center justify-center bg-background px-4 py-12">
      <div className="absolute end-4 top-4">
        <ThemeToggle />
      </div>
      <main className="stagger-in relative flex w-full max-w-sm flex-col gap-6">
        <PaluiMark className="mx-auto size-12" />
        {children}
      </main>
    </div>
  )
}

// The command shown when locked out or claiming the dashboard.
export const INVITE_COMMAND = "docker compose exec app palui owner invite"
