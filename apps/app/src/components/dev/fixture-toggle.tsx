// Dev only: the "Demo data / Worst case" switch from the `break-ui` skill. It
// swaps the dataset behind the whole dashboard (src/dev/fixtures) so every page
// can be checked against the realistic worst case without touching a component.
// It renders nothing unless the database was filled by a fixture, and the shell
// only mounts it in a dev build.
import { useRouter } from "@tanstack/react-router"
import * as React from "react"
import { getFixtureState, setFixture } from "@/lib/fns/dev-fixtures"
import "./fixture-toggle.css"

const SEGMENTS = [
  { name: "demo", label: "Demo data" },
  { name: "worst", label: "Worst case" },
  { name: "empty", label: "Empty" },
  { name: "one", label: "One" },
  { name: "huge", label: "1,000 rows" },
] as const

type Name = (typeof SEGMENTS)[number]["name"]

export default function FixtureToggle() {
  const router = useRouter()
  const [current, setCurrent] = React.useState<Name | null>(null)
  const [pending, setPending] = React.useState<Name | null>(null)
  const [error, setError] = React.useState<string | null>(null)

  React.useEffect(() => {
    let alive = true
    getFixtureState()
      .then((s) => alive && setCurrent(s.current))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

  if (!current) return null

  const pick = async (name: Name) => {
    if (name === current || pending) return
    setPending(name)
    setError(null)
    try {
      await setFixture({ data: { name } })
      // Chat ids differ per dataset, so a selected chat can't carry over.
      if (router.state.location.pathname.endsWith("/chats")) await router.navigate({ to: "/chats", search: {}, replace: true })
      // Re-run every loader in place. Not a page reload: each reload would open
      // a fresh live-events stream, and a few of those in a row use up the
      // browser's connections to localhost.
      await router.invalidate()
      setCurrent(name)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setPending(null)
    }
  }

  return (
    <div className="fixture-toggle" role="group" aria-label="Fixture data (dev only)">
      {SEGMENTS.map((s) => (
        <button key={s.name} type="button" className="fixture-toggle-item" aria-pressed={s.name === (pending ?? current)} disabled={pending !== null} onClick={() => void pick(s.name)}>
          {s.label}
        </button>
      ))}
      {error && (
        <p className="fixture-toggle-error" role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
