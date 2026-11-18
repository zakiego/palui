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
2. Put the bullet under the feature category it belongs to, creating the heading if needed. Headings go in this order, and only these exist (`CATEGORIES` in `apps/site/lib/changelog.ts`):
   - `### Breaking`: anything the owner has to do on upgrade. Always first, entries have no type.
   - `### Chat`: channels, groups, chat commands, media, contacts, attachments.
   - `### Agents`: agents, members, models, memory and recall, budgets.
   - `### Tools`: built-in tools, MCP servers, connectors, approvals and permissions.
   - `### Automations`: flows, routines, reminders, jobs, webhooks.
   - `### Dashboard`: pages and the look that aren't about one feature above.
   - `### API`: the REST API and the MCP server at `/mcp` for outside clients.
   - `### Self-hosting`: deploy, upgrade, `/health`, backups, the CLI.
   - `### Site`: the landing page, these docs, the brand page.
3. Start the bullet with its type: `Added: `, `Changed: ` or `Fixed: `. Inside a category, keep Added, then Changed, then Fixed. The type picks the version bump (`pnpm release suggest` reads it), so mark it honestly: a new feature marked Fixed would ship as a quiet patch. The docs page renders the type as a label.
4. One bullet per change, kept short. One sentence of about 15 words, two short sentences at most. Written for the owner running Palui:
   - Say what they'll notice, not how it works. "Snoozed reminders fire in your time zone, not UTC." beats "fix(reminders): use tz in snooze", and it also beats a paragraph about why it broke.
   - The why, the limits and the edge cases go in the docs page and the PR body, not here. The PR link takes the reader there.
   - Name the surface: chat command, dashboard page, connector, `/health`, CLI.
   - Breaking bullets end with what to do on upgrade ("Add `FOO_URL` to `.env` before you deploy."). Several steps go in a nested list under the bullet.
   - A migration gets "Adds a migration." The changelog's intro says what that means for rollback.
   - Plain English, no em dashes, no bold, no marketing words.
5. End the bullet with its PR link: `([#123](https://github.com/zakiego/palui/pull/123))`. A change that took several PRs lists them: `([#85](...), [#115](...))`. The number only exists once the PR is open, so open the PR first (`palui-pr`), then add the link and push it as a follow-up commit (`docs(changelog): link the PR`).
6. Check it parses:
   ```bash
   pnpm release check
   ```
   Every released entry must end with its PR link, and every entry needs a known category and a type, or the test fails. An Unreleased entry may wait for its link until the PR is open, but the nightly release won't cut one without it.

## Example

```mdx
## Unreleased

### Chat

- Fixed: Voice notes longer than 2 minutes no longer fail transcription. ([#151](https://github.com/zakiego/palui/pull/151))

### Tools

- Added: Web fetch can read PDFs up to 5 MB. ([#150](https://github.com/zakiego/palui/pull/150))
```
