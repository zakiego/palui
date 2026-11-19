# Security policy

Palui holds an owner's chats, contacts and API keys, so security reports are welcome and taken seriously.

## Reporting a vulnerability

Please don't open a public issue. Report it privately through GitHub: go to the [Security tab](https://github.com/zakiego/palui/security/advisories/new) and choose "Report a vulnerability".

Include what you found, how to reproduce it, and what an attacker could do with it. A proof of concept helps but isn't required.

You'll get a reply within 7 days. Once a fix ships, the advisory is published with credit to you, unless you'd rather stay anonymous.

## Supported versions

Only the latest release gets security fixes. Palui is pre-1.0, so upgrade to the newest version before reporting. Your version is `version` in `GET /health`.

## Scope

In scope:

- The `palui` server and dashboard (`apps/server`, `apps/app`)
- The host bridge (`apps/host-bridge`)
- The edge worker and docs site (`apps/edge`, `apps/site`)

Out of scope:

- Third-party services Palui connects to (model providers, MCP servers, WhatsApp, Telegram)
- Attacks that need the owner's `MASTER_KEY`, a valid session, or shell access to the host
- The host bridge's `dangerouslyAllowAnyCommand` mode doing what its name says

## How Palui protects data

The [security docs](https://palui.zakiego.com/docs/security) explain the owner model, approvals for destructive tools, the taint rule for untrusted content, and how secrets are encrypted at rest.
