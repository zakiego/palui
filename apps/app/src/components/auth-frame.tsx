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
        <span
          aria-hidden="true"
          className="mx-auto flex size-12 items-center justify-center rounded-xl bg-primary text-primary-foreground shadow-[0_1px_2px_oklch(0_0_0/0.12),inset_0_1px_0_oklch(1_0_0/0.15)]"
        >
          <PaluiMark className="size-8" />
        </span>
        {children}
      </main>
    </div>
  )
}

// The command shown when locked out or claiming the dashboard.
export const INVITE_COMMAND = "docker compose exec app palui owner invite"
