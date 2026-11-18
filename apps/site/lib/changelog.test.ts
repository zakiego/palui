import { readFileSync } from "node:fs"
import { describe, expect, it } from "vitest"
import { bumpVersion, checkChangelog, compareVersions, cutRelease, parseChangelog, suggestBump } from "./changelog"

const CHANGELOG = readFileSync(new URL("../content/docs/changelog.mdx", import.meta.url), "utf8")
const ROOT_PKG = JSON.parse(readFileSync(new URL("../../../package.json", import.meta.url), "utf8")) as { version: string }

const pr = (n: number) => `([#${n}](https://github.com/zakiego/palui/pull/${n}))`
const sample = (unreleased: string, releases = `## 0.1.0 (2026-11-11)\n\n- First. ${pr(1)}\n`) =>
  `---\ntitle: Changelog\n---\n\nIntro.\n\n## Unreleased\n\n${unreleased}\n\n${releases}\n## Not in v1\n\n- Later.\n`

describe("the real changelog", () => {
  it("matches the root package.json version and every rule", () => {
    expect(checkChangelog(CHANGELOG, ROOT_PKG.version)).toEqual([])
  })
})

describe("checkChangelog", () => {
  it("passes a well-formed file", () => {
    expect(checkChangelog(sample("Nothing yet."), "0.1.0")).toEqual([])
  })

  it("catches a version bumped without a release", () => {
    expect(checkChangelog(sample("- New thing."), "0.2.0").join()).toMatch(/package.json says 0.2.0/)
  })

  it("catches a sloppy heading", () => {
    expect(checkChangelog(sample("Nothing yet.", "## v0.1.0 - 2026-11-11\n\n- First.\n"), "0.1.0").join()).toMatch(/should look like/)
  })

  it("catches releases out of order, a fake date and an empty release", () => {
    const releases = "## 0.1.0 (2026-11-11)\n\n- One.\n\n## 0.2.0 (2026-02-30)\n\nNothing.\n"
    const problems = checkChangelog(sample("Nothing yet.", releases), "0.1.0").join("\n")
    expect(problems).toMatch(/0.2.0 is listed below 0.1.0/)
    expect(problems).toMatch(/not a real date/)
    expect(problems).toMatch(/has no bullet points/)
  })

  it("wants every released entry to end with its PR link", () => {
    const releases = `## 0.1.0 (2026-11-11)\n\n- Linked. ${pr(4)}\n- Two PRs. ([#5](https://github.com/zakiego/palui/pull/5), [#6](https://github.com/zakiego/palui/pull/6))\n- Bare number. (#7)\n- Wrong link. ([#8](https://github.com/zakiego/palui/pull/9))\n- No link.\n  - A sub-step needs none.\n`
    const problems = checkChangelog(sample("- Unreleased can wait for its PR.", releases), "0.1.0")
    expect(problems).toHaveLength(3)
    expect(problems.join("\n")).toMatch(/line 15: 0.1.0 entry doesn't end with its PR link/)
  })

  it("ignores headings inside code fences", () => {
    expect(checkChangelog(sample("```md\n## 9.9.9\n```"), "0.1.0")).toEqual([])
  })
})

describe("cutRelease", () => {
  it("moves Unreleased under the new heading and empties it", () => {
    const next = cutRelease(sample(`### Fixes\n\n- A fix. ${pr(2)}`), "0.1.1", "2026-11-12")
    const parsed = parseChangelog(next)
    expect(parsed.unreleased).toBe("Nothing yet.")
    expect(parsed.releases.map((r) => r.version)).toEqual(["0.1.1", "0.1.0"])
    expect(parsed.releases[0]!.body).toBe(`### Fixes\n\n- A fix. ${pr(2)}`)
    expect(checkChangelog(next, "0.1.1")).toEqual([])
  })

  it("leaves an entry without a PR link for the check to catch", () => {
    const next = cutRelease(sample("### Fixed\n\n- A fix."), "0.1.1", "2026-11-12")
    expect(checkChangelog(next, "0.1.1").join()).toMatch(/doesn't end with its PR link/)
  })

  it("refuses to cut a release with nothing in it", () => {
    expect(() => cutRelease(sample("Nothing yet."), "0.1.1", "2026-11-12")).toThrow(/nothing to release/)
  })
})

describe("versions", () => {
  it("bumps and compares", () => {
    expect(bumpVersion("0.1.9", "patch")).toBe("0.1.10")
    expect(bumpVersion("0.1.9", "minor")).toBe("0.2.0")
    expect(bumpVersion("0.9.3", "major")).toBe("1.0.0")
    expect(compareVersions("0.10.0", "0.9.0")).toBeGreaterThan(0)
  })
})

describe("suggestBump", () => {
  const with_ = (body: string) => sample(body)
  it("has nothing to suggest when Unreleased is empty", () => {
    expect(suggestBump(with_("Nothing yet."), "0.1.0").bump).toBeNull()
  })
  it("picks patch for fixes only", () => {
    expect(suggestBump(with_("### Fixed\n\n- A fix."), "0.1.0").bump).toBe("patch")
  })
  it("picks minor for anything added or changed", () => {
    expect(suggestBump(with_("### Added\n\n- A thing.\n\n### Fixed\n\n- A fix."), "0.1.0")).toEqual({ bump: "minor", reason: "has Added" })
    expect(suggestBump(with_("### Changed\n\n- A change."), "1.2.0").bump).toBe("minor")
  })
  it("keeps breaking changes on minor before 1.0 and major after", () => {
    expect(suggestBump(with_("### Breaking\n\n- Add FOO."), "0.4.0").bump).toBe("minor")
    expect(suggestBump(with_("### Breaking\n\n- Add FOO."), "1.4.0").bump).toBe("major")
  })
  it("treats unsorted bullets as a minor", () => {
    expect(suggestBump(with_("- Something."), "0.1.0").bump).toBe("minor")
  })
})
