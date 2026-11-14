// Dev only: the fixture toggle (src/components/dev). The server answers
// `current: null` outside a dev build and refuses setFixture there; the toggle
// itself is only imported in a dev build.
import { noInput, withInput } from "./client"
import type { FixtureName } from "./types"

// `current` is null when the database wasn't filled by a fixture, which is
// also what hides the toggle.
export const getFixtureState = noInput<{ current: FixtureName | null }>("getFixtureState", "GET")
export const setFixture = withInput<{ name: FixtureName }, { current: FixtureName }>("setFixture", "POST")
