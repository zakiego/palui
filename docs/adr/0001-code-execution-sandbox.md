# 0001: Code execution sandbox (Kandang)

Status: accepted by the owner, 2026-11-19. Not built yet. Research and measurements: `docs/research/code-execution-sandbox.md`.

This is the first ADR in this repo. It follows the Context, Decision, Consequences, Alternatives layout; later ADRs take the next number (`0002-...`).

## Context

Palui only has fixed tools. It can't run code, a shell or an arbitrary HTTP call, so every new need takes a new tool first. Example: it can't answer "check my ElevenLabs quota" even though the voice connector already holds an ElevenLabs key.

The ask is a capability like claude.ai's code execution: a throwaway Linux box with bash and Python, where chat attachments go in, the model writes and runs code, and output files (charts, cropped photos, xlsx, pdf, docx) come back to the chat.

What the research found:

- Anthropic's server-side code execution has no network, so it can't make the API calls that motivated this. Its files go through the Files API, which CLIProxyAPI doesn't route. It also only works on Claude models.
- OrbStack on the Mac mini has no KVM, so microVMs (Firecracker, E2B, Kata) are out.
- Whoever holds the OrbStack Docker socket can mount the Mac home folder, SSH keys included (tested read-only).
- gVisor (`runsc`, systrap) works on the Mac mini inside an isolated OrbStack machine, which has no Mac files and can't reach Palui. Docker doesn't run in such a machine (it blocks the device cgroup BPF call), so `runsc` is driven directly with per-run cgroups. Measured: 25 to 36 ms to start, a pandas chart in 3.5 s at up to 225 MB, and the 1 GB memory cap, the process cap, a timeout and no-network all held.
- The image (Alpine, bash, numpy, pandas, matplotlib, Pillow, openpyxl, pypdf, python-docx, requests) is 170 MB.

## Decision

1. Build Kandang, a small separate Go service in this repo (`apps/sandbox`). Kandang is the product name; the folder, the connector id (`sandbox`) and the tool name (`run_code`) stay in English.
2. Kandang runs inside its own isolated OrbStack machine (`--isolated --isolate-network`) on the Mac mini, with a firewall rule that drops private address ranges. No Docker socket anywhere: not in Palui, not in Kandang.
3. Every run is a gVisor sandbox: a read-only rootfs unpacked from the image, tmpfs `/tmp` and `/work`, uid 10001, no capabilities, `noNewPrivileges`, no network, and a per-run cgroup with 1 GB RAM, no swap, one CPU and a pids cap. 60 s default timeout, 300 s max. Two runs at once.
4. Kandang is agnostic. It never holds or receives secrets and never calls APIs for Palui. Credentials live only in Palui's `http_request` tool.
5. Palui gets a first-party `sandbox` connector with one tool, `run_code` (class `read`, data scope `chat`). It needs no approval, in chats, jobs and flows alike. Files go in through attachment refs and come back through the attachments store and `send_media`. Every run lands in a `sandbox_runs` audit table in Palui's DB.
6. Ship `http_request` (with named credentials) first. It answers the quota question now.
7. No Anthropic server-side code execution, and no separate product or repo for now.

## Consequences

- Strongest isolation available on this host: a gVisor user-space kernel, inside a machine that can't see the Mac's files or reach Palui. An escape has to beat gVisor and then still lands somewhere with nothing in it.
- Kandang owns more plumbing than a Docker-based design: OCI bundles, cgroups and cleanup are its own code (my estimate: a few hundred more lines of Go than the Docker SDK path). That's the price of no socket.
- Python work runs about 20 to 25% slower than under runc (gVisor intercepts syscalls). Starts are about 5 times faster, since Docker is out of the path.
- gVisor counts its own threads against `pids.max`, so 128 allowed about 50 processes. Fine for scripts; raise it if a job needs more.
- One more OrbStack machine, about 1.5 GB of disk with the rootfs. The disk was cleaned from 7.1 GB to 22 GB free on 2026-11-19.
- Images are built with Docker elsewhere (main engine or CI) and unpacked into the machine, so the deploy step is new: build, export, copy, swap the rootfs.
- Without secrets or network, Kandang can't fetch anything itself. The model calls `http_request` first and passes the data in as a file. That's one more step for some asks, and it keeps every credential behind Palui's approval rules.
- The model gets a general tool, so it will reach for code when a typed tool would do. Tool descriptions should steer single API calls to `http_request` and keep `run_code` for computation and files.
- It's model-agnostic. Switching a role to Gemini or the OpenAI protocol keeps the feature.

## Alternatives considered

- Anthropic code execution through the model route: no network, no Files API route through CLIProxyAPI, Claude-only, data kept 30 days. Rejected.
- Docker socket inside Palui: least code, but a bug in Palui would give Mac home access. Rejected.
- Hardened Docker (runc) on the main OrbStack engine, gVisor later: fastest to build, but the service would hold a socket that reaches the Mac home folder, and runc shares the kernel. Rejected now that gVisor is proven here.
- gVisor through Docker on the main engine: needs a daemon config change and a restart of the engine that runs every live app, and still needs the socket. Rejected.
- Docker inside a non-isolated OrbStack machine: works, but that machine mounts the whole Mac home folder. Rejected.
- Firecracker, E2B, Kata: need KVM, which OrbStack doesn't offer.
- microsandbox on the Mac host: VM isolation without KVM, but beta, outside OrbStack, and macOS-only.
- nsjail, bubblewrap or `srt`: no daemon, but a weaker boundary than gVisor.
- Judge0 or Piston: built for judging submissions. Judge0 has cgroup v2 problems, and neither handles files in and out well.
- No container at all (goja or CPython on wazero inside a Go process): no charts, photo edits or documents, which is most of the ask.
- Secrets in Kandang (`run_code_online`): would let code call APIs directly, but puts credentials inside the box and needs an egress proxy. Rejected by the owner: Kandang stays agnostic.
- `http_request` only: covers API calls, not charts, documents or images. Kept as phase 0, not as the whole answer.
- A separate open-source product now: there's no second client yet.

## Answered by the owner (2026-11-19)

1. Secrets: none in Kandang. It's agnostic.
2. Approvals: `run_code` runs without approval, also in jobs and flows.
3. Image: Alpine (170 MB with bash and the font cache).
4. Disk: cleaned before phase 1 (done, 22 GB free).
5. Isolation: do the best available, time isn't the constraint. That's gVisor from day one in an isolated machine.

## Still open (not needed for phase 1)

1. Network: should runs ever get internet (an allowlist proxy, `pip install`)? Today: never.
2. Process cap: is about 50 processes per run enough, or raise `pids.max`?
