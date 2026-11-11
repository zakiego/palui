---
name: palui-changelog
description: Add a Palui Agent change to the Unreleased section of the docs changelog (apps/site/content/docs/changelog.mdx). Use whenever a Palui branch changes something a self-hoster or the owner would notice (a feature, a fix, a behavior or config change, a new connector, a migration) and before opening its PR, or when asked to "add a changelog entry", "update the changelog" or "note this in the changelog".
---

# Palui changelog entry

Every PR that changes what Palui does for the person running it adds its line to `## Unreleased` in `apps/site/content/docs/changelog.mdx`, in the same PR. The release skill (`palui-release`) later moves that section under a version. Never write a version heading yourself, and never bump `package.json`.

## Does this PR need one?

Yes: features, fixes a user could hit, changed behavior or defaults, new or removed chat commands, API routes, env vars, connectors, dashboard pages, anything that changes deploy or rollback (migrations included).

No: refactors with no visible effect, tests, internal tooling, typo fixes in code comments, agent skills. When unsure, add it. Cutting a line at release time is cheap; finding a missing one isn't.

## How to write it

1. Open the file and find `## Unreleased`. If its body is `Nothing yet.`, replace that line.
2. Put the bullet under one of these subheadings, creating it if needed, in this order: `### Breaking`, `### Added`, `### Changed`, `### Fixed`. The heading picks the version bump (`pnpm release suggest` reads it), so file it honestly: a new feature under Fixed would ship as a quiet patch.
3. One bullet per change, one or two sentences, written for the owner running Palui:
   - Say what they'll notice, not which file changed. "Reminders snoozed to tomorrow now fire at 08.00 in your zone, not 08.00 UTC." beats "fix(reminders): use tz in snooze".
   - Name the surface: chat command, dashboard page, connector, `/health`, CLI.
   - Breaking bullets end with what to do on upgrade ("Add `FOO_URL` to `.env` before you deploy.").
   - A migration gets a note when it matters for rollback: "Adds a migration; rolling back past this release needs the pre-deploy snapshot."
   - Plain English, no em dashes, no bold, no marketing words.
4. Check it parses:
   ```bash
   pnpm release check
   ```

## Example

```mdx
## Unreleased

### Added

- Web fetch can read PDFs up to 5 MB and hands the model their text.

### Fixed

- A WhatsApp voice note longer than 2 minutes no longer fails transcription.
```
