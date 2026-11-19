# Research: code execution sandbox

Status: research, 2026-11-19, updated the same day with the owner's answers and a gVisor test. Nothing built. Decision draft: `docs/adr/0001-code-execution-sandbox.md`. Author: Palui agent session. Reviewer: the owner.

## TL;DR

- Palui can't run code today, so every new need ("check my ElevenLabs quota") waits for a new tool. The ask is a throwaway Linux box with bash and Python where files go in, the model writes and runs code, and files come back.
- Anthropic's server-side code execution tool doesn't fit. It has no internet access at all, so it can't call ElevenLabs or any other API. Its files go through the Files API, and CLIProxyAPI has no `/v1/files` route. It would also tie the feature to Claude models.
- The Mac mini has no KVM inside OrbStack (checked), so Firecracker, E2B and every other microVM option is out.
- gVisor works on the Mac mini (tested 2026-11-19): `runsc` systrap, driven directly with no Docker, inside an isolated OrbStack machine that can't see the Mac's files or reach Palui. A run starts in 25 to 36 ms, and the memory, process, time and network limits all held. So the plan skips hardened Docker and starts on gVisor.
- Measured on the Mac mini (arm64, OrbStack): the Kandang image (Alpine, bash, numpy, pandas, matplotlib, Pillow, openpyxl, pypdf, python-docx, requests) is 170 MB. A light run peaks at about 50 MB, a pandas chart at 170 to 225 MB (3.5 s), a 12 MP photo edit at 145 MB.
- Recommendation: a small separate Go service, Kandang (`apps/sandbox` in this repo), running inside its own isolated OrbStack machine. No Docker socket anywhere. Palui gets a first-party `sandbox` connector for files, approvals and the audit log. Not its own product yet.
- Kandang is agnostic: it never holds secrets or calls APIs for Palui. Credentials live only in Palui's `http_request` tool, which ships first. It answers the quota question in about two days.

## 1. Anthropic: code execution tool, Files API, Artifacts

Source: the code execution tool docs (links at the end), read 2026-11-19.

| Thing | Value |
|---|---|
| Tool types | `code_execution_20250825` (bash and files), `code_execution_20260120` (adds REPL state and programmatic tool calling), `code_execution_20260521` (same runtime, tells the model about the 90 s cell limit). No beta header needed. |
| Runtime | Linux, x86_64, Python 3.11 |
| Limits | 1 CPU, 5 GiB RAM, 5 GiB disk. A whole call past the max time returns `execution_time_exceeded` |
| Network | None. "Completely disabled", no outbound requests, no `pip install` at runtime |
| Preinstalled | pandas, numpy, scipy, scikit-learn, statsmodels, matplotlib, seaborn, pyarrow, openpyxl, xlsxwriter, xlrd, pillow, python-pptx, python-docx, pypdf, pdfplumber, pypdfium2, pdf2image, reportlab, sympy and more; CLI tools like ripgrep, sqlite, 7zip |
| Files in | Upload with the Files API (`POST /v1/files`), then a `container_upload` block with the `file_id` |
| Files out | Each bash call gets `$OUTPUT_DIR`; files left there come back as `file_id`s, downloaded through the Files API |
| Containers | Reusable by passing `container` id. Checkpointed after about 5 min idle, expire 30 days after creation |
| Pricing | Free when `web_search_20260209` or `web_fetch_20260209` is in the request. Otherwise billed by execution time, 5 min minimum, 1,550 free hours per org per month, then $0.05 per container hour. Files in the request bill even if the tool isn't called |
| Retention | Not ZDR eligible, container data kept up to 30 days |
| Platforms | Claude API, Claude Platform on AWS, Google Cloud (Anthropic managed), Foundry hosted on Anthropic. Not Bedrock or Vertex |

Artifacts (claude.ai) are a different thing: HTML or React rendered in the user's browser. Nothing runs server-side for them, and they can't call private APIs with stored keys. Not relevant to Palui's channels (WhatsApp, Telegram).

### Can Palui use it through its current route?

Probably not, and it wouldn't help if it could:

1. Route. Palui calls `POST {base}/v1/messages` with `x-api-key` (`apps/server/internal/models/anthropic.go:813`). The base is CLIProxyAPI, which signs in to Claude with subscription OAuth. CLIProxyAPI 8.0.72's binary lists routes for `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, chat completions, responses and media, but no `/v1/files` (checked with `strings` on the installed binary). So `container_upload` inputs and output downloads can't work. The binary does mention `code_execution` result types, so the tool block itself may pass through. Whether a subscription OAuth token is allowed to run server tools was not tested (that would spend account quota and wasn't asked for).
2. No network. The motivating example (an ElevenLabs quota check) needs an outbound call with a stored key. The Anthropic sandbox can't make one. Putting the key into the prompt so the model could use it elsewhere would break PLAN 10's secrets rule anyway.
3. Portability. It only exists on Claude models and Anthropic-run platforms. CLIProxyAPI also serves Gemini; a role switched to Gemini or `openai` protocol would silently lose the tool. A self-hosted sandbox is a normal Palui tool and works with any model.
4. Data. Files Palui sends would sit in Anthropic's container store for up to 30 days. Fine for charts, worse for the owner's documents.

Worth keeping in mind: Anthropic's Managed Agents vaults inject a secret at egress so the sandbox only holds a placeholder. Kandang doesn't need it, since it holds no secrets (section 6.4).

## 2. Self-hosted isolation options

Host facts first, because they rule out half the list:

- Mac mini: OrbStack's Linux VM (kernel 7.2, cgroup v2, arm64). A privileged container sees no `/dev/kvm`, and OrbStack doesn't support nested virtualization (open issue).

| Option | Boundary | Start | Overhead | Works here | Maintenance | Fit with Go |
|---|---|---|---|---|---|---|
| Hardened Docker (runc) | Shared kernel; namespaces, seccomp, cap-drop, cgroups | 141 to 191 ms measured | 0.25 MB per idle container measured | Yes | Low: patch Docker and runc (2025 runc breakout CVEs fixed in 1.2.8 and 1.3.3) | Official Docker Go SDK |
| gVisor `runsc` (systrap) | User-space kernel in front of the host kernel | 25 to 36 ms measured (direct `runsc run`, no Docker) | About 10 MB per idle sandbox measured | Yes, tested in an isolated OrbStack machine (section 5) | Medium: another runtime to update; some syscalls missing, file I/O slower | Shell out to `runsc` with a generated OCI bundle |
| Firecracker, Kata, Cloud Hypervisor | VM | About 125 ms (Firecracker spec) | About 5 MB plus guest RAM | No: needs KVM | High | No good Go SDK |
| microsandbox (libkrun) | VM through macOS Hypervisor.framework | Under 100 ms claimed | Not found | Only on the Mac host itself, outside OrbStack. Beta | High, beta API | Go SDK exists |
| nsjail, bubblewrap | Namespaces plus seccomp, no daemon | A few ms | Negligible | Yes | Medium: you write the mounts and policy | Spawn a subprocess |
| Anthropic `sandbox-runtime` (`srt`) | bubblewrap on Linux, Seatbelt on macOS, egress through a domain-filtering proxy | Fast | Negligible | Yes | Beta, Node dependency | Shell out to a Node CLI |

Open-source sandbox products:

- E2B: Firecracker underneath, so it needs KVM. Out.
- Daytona: went closed and non-self-hostable in mid 2026, public repo archived. Out.
- Judge0: built on `isolate`; cgroup v2 support is still an open issue, and OrbStack is v2. Built for judging submissions, not files in and out.
- Piston: self-hostable, `isolate` inside Docker, single-shot runs only. Possible but adds a second runtime for little gain.
- Jupyter Kernel Gateway: manages kernels, isolates nothing by itself. Could sit inside a container later for session state.
- Pyodide in Deno (langchain-sandbox, mcp-run-python): Python only, no bash, and the langchain one is archived after a published escape. Not a security boundary.
- open-interpreter and similar: run code on the host. Not a sandbox.

Takeaway: there's no off-the-shelf project that fits better than about 500 lines of Go on the Docker SDK. Docker gives the files API (`CopyToContainer`, `CopyFromContainer`), limits and a mature runtime; gVisor drops in later behind the same calls.

## 3. Built into Palui or a separate service

The hypotheses from the chat, checked:

| Hypothesis | Verdict |
|---|---|
| A Docker socket in Palui is root on the host next to the DB, the WhatsApp session and the tokens | True, and worse than stated. On the Mac mini, a container started through the OrbStack socket mounted `~/.ssh` (29 entries listed, read-only test). Socket access means access to the Mac home folder: SSH keys, CLIProxyAPI config, every repo. Palui runs model-steered code paths and parses untrusted input, so it must never hold that socket. |
| A runaway job shouldn't take Palui down | Mostly handled by cgroup limits, not by process separation. A job at 2 GB under a 1 GB cap was OOM-killed alone (exit 137); 300 processes under a 128 pid cap failed with "Cannot fork". What separation doesn't fix: both share the OrbStack VM's 8 GB and 2 CPUs, and the disk. That's a concurrency cap and a host choice, covered below. |
| Other agents and apps could reuse it | Plausible (the Oslo Notebook assistant), but no second user is lined up. Cheap to keep open with a clean HTTP API, not worth designing for now. T3 already has a shell of its own. |
| Easy to swap or move | True for a separate service: Palui only knows a URL and a token. |

Comparison:

| | Inside Palui | Separate service (recommended) | Separate product/repo |
|---|---|---|---|
| Security boundary | Palui holds the socket: one bug in Palui gives Mac home access | No socket at all. The service runs gVisor inside an isolated OrbStack machine with no Mac files. Its API accepts code, files and limits, never runtime options or mounts | Same as separate service |
| Resource isolation | Container limits | Same, plus its own queue and restart | Same |
| Deploy | Nothing new | One more compose project and image, auto-deployed from the same repo | Own repo, CI, releases, versioning against Palui |
| Reuse | None | Any client with a token | Designed for it |
| Cost now | Lowest code, worst risk | About a day more than inside | Several days more, plus upkeep |

Recommendation: a separate service, kept in this monorepo as `apps/sandbox` (a second Go binary, like `apps/host-bridge`), running in its own isolated OrbStack machine (section 5). Its product name is Kandang (Indonesian for a pen or cage). The name is for people only: the folder stays `apps/sandbox`, the connector stays `sandbox` and the tools stay `run_code`. Extract it into its own repo only when a second real client exists. Palui talks to it through a first-party `sandbox` connector rather than the generic MCP connector, because the connector has to write the audit row, apply Palui's approval rules and turn output files into attachments. An MCP endpoint on the service can come later for other agents.

## 4. Measured footprint

Measured 2026-11-19 on the Mac mini (M4, OrbStack, arm64, Docker 29.8.2). Sizes are uncompressed image sizes from `docker image inspect`. Each run used: `--network none --read-only --cap-drop ALL --security-opt no-new-privileges --pids-limit 128 --memory 1g --memory-swap 1g --cpus 1 --tmpfs /tmp:size=256m`, non-root user. Scripts are in section 9.

### Image size

| Image | Size | Added |
|---|---|---|
| `python:3.12-slim-bookworm` | 149 MB | |
| Lean: numpy, pandas, matplotlib, Pillow, openpyxl, pypdf, python-docx, requests | 425 MB | +276 MB |
| Full: lean + scipy, scikit-learn, seaborn, pdfplumber, xlsxwriter, python-pptx, reportlab, sympy | 761 MB | +336 MB |
| Lean + ffmpeg | 814 MB | +389 MB |
| Lean + LibreOffice (writer, calc, no GUI) | 813 MB | +388 MB |
| Lean + Chromium (Debian package) + fonts | 1,109 MB | +684 MB |

Everything at once would be about 2.2 GB. Build times were 29 s (lean) to 202 s (Chromium).

### Memory and time

| Measurement | Result |
|---|---|
| dockerd RSS in the OrbStack VM | 141 MB (already running for the other apps, so 0 extra) |
| containerd RSS | 32 MB (same) |
| Sandbox service itself | Not built. Palui's Go binary idles at about 10 MiB (`docs/GO-PORT.md`), expect similar |
| Idle container (`sleep`) | 0.25 MB |
| Cold start, `python -c pass` | 141, 141, 144, 152, 191 ms |
| Warm `docker exec`, `python -c pass` | 48 to 103 ms |
| Light run (math on 100k floats, import requests) | peak 29 MB, 282 ms cold; 22 MB, 131 ms warm |
| Chart (500k-row pandas groupby, matplotlib PNG, 50k-row xlsx) | peak 155 to 255 MB; 2.8 s cold with no font cache, 1.4 to 1.5 s with it baked, 1.3 s warm |
| Photo edit (12 MP crop, blur, resize, JPEG) | peak 135 MB, about 300 ms |
| `import pandas, sklearn` (full image) | 750 to 920 ms |
| 2 GB allocation under a 1 GB cap | OOM-killed, exit 137, nothing else affected |
| 300 background processes under `--pids-limit 128` | "Cannot fork", contained |
| Busy loop under `timeout 3` | exit 124 |

### Palui's estimates against the numbers

| Estimate | Measured | Verdict |
|---|---|---|
| Image 0.6 to 1 GB base | 0.43 GB lean, 0.76 GB full | Lower than guessed for lean, in range for full |
| Idle 20 to 50 MB | Container 0.25 MB; service likely 10 to 20 MB; Docker already running | Lower |
| Light run 30 to 80 MB | 22 to 29 MB | Lower |
| Chart/pandas 100 to 500 MB | 155 to 255 MB at 500k rows | In range |
| Per-run limit 512 MB to 1 GB, 1 CPU | 1 GB is enough for everything tested; 512 MB would be tight for big pandas jobs | Use 1 GB default |
| 1 to 2 concurrent | OrbStack has 2 CPUs, so 2 runs at 1 CPU each take everything | Use 2, with a queue |
| Cold start 0.5 to 1.5 s | Container 0.15 s; end to end 0.3 s (light) to 1.5 s (chart) | Container start is faster; imports dominate |

A warm pool saves about 100 ms of container start and is not worth it for the MVP. Baking the matplotlib font cache saves 1.3 s on the first chart, and is.

### How small can it get

Measured the same day with the same run flags. Every image ran the same jobs, so each row is a working image, not only a size.

| Variant | Size | Can do | Light run (cold) |
|---|---|---|---|
| Lean on Debian slim (the baseline above) | 425 MB | Everything in the lean set | 282 ms |
| Same on Debian slim, stripped (multi-stage, no test folders, no `.pyc`) | 322 MB | Same | 579 ms |
| Lean on distroless (`gcr.io/distroless/python3-debian13`, Python 3.13.5, packages copied in from a build stage, stripped) | 245 MB | The same Python jobs: chart 2.3 to 2.5 s at 160 MB peak, photo edit 416 ms. No shell, so no bash and no CLI programs | 436 ms |
| Lean on Alpine: numpy, pandas, matplotlib, Pillow and requests from Alpine's packages, openpyxl, pypdf and python-docx from pip, stripped | 157 MB | Same. Chart 2.8 s at 160 MB peak, photo edit 527 ms at 141 MB peak | 532 ms |
| Distroless + requests, Pillow, openpyxl, pypdf, python-docx | 113 MB | API calls, photo edits, xlsx, pdf, docx, from Python only | 665 ms |
| Alpine + requests, Pillow, openpyxl, pypdf, python-docx (no numpy, pandas or matplotlib) | 90 MB | API calls, photo edits, xlsx, pdf, docx. No charts, no dataframes | 517 ms |
| Alpine + requests | 63 MB | bash, the Python standard library, API calls | 384 ms |
| `python:3.12-alpine` alone | 53 MB | | |

What the small images cost:

- Removing `.pyc` files saves space but adds about 100 to 300 ms per run, because Python compiles every import again in a fresh container.
- Alpine uses musl. matplotlib has no musl arm64 wheel on PyPI (pip tried to compile it and failed), so the heavy packages come from Alpine's own repository at Alpine's versions (Python 3.14.10, numpy 2.4.6, pandas 3.0.3, matplotlib 3.10.6 on the day). A package the model tries to `pip install` at run time is more likely to have no wheel.
- Container start doesn't change (about 140 to 230 ms).
- Distroless has no shell and no package manager. That takes away the tools an attacker would reach for after a break-in, and it keeps pip's own package versions (glibc wheels, so none of Alpine's gaps). But the ask includes bash, and code that shells out fails (`FileNotFoundError: 'ls'`). There's no `pip install` at run time either. The bases alone: distroless Debian 13 is 73 MB, Debian 12 is 65 MB (Python 3.11.2), Chainguard's Python image is 73 MB (Python 3.15.2).

Without Docker at all, from a scratch Go program (wazero v1.12.0 and goja, both pure Go):

| Variant | Size | Result |
|---|---|---|
| JavaScript inside the Go process (goja) | 0 MB: no image, no service, no Docker socket | The light job took 42 to 47 ms. A busy loop was interrupted at 502 ms. No Python, no bash, no file libraries. HTTP would be a host function that applies the allowlist and adds secrets |
| CPython built for WebAssembly, run by wazero | 26 MB file plus a 47 MB compile cache | Compile once in 2.0 s (0.6 s from cache), 65 to 75 ms per start, light job 712 to 748 ms. A 256 MiB cap gave a clean `MemoryError`, there are no sockets and no host files, and a 2 s timeout stopped a busy loop after 3.0 s. The host process used 177 MB. Standard library only: no numpy, pandas, Pillow or matplotlib, and no bash. The build is from December 2023 (Python 3.12.0) |

Takeaway: the Alpine build does everything the 425 MB image did at 37% of the size, and is the default to start with; fall back to Debian slim if missing musl wheels get in the way. Distroless sits in between at 245 MB and only fits if bash is dropped. The options without Docker are far smaller and need no Docker socket, but they can't make charts, edit photos or build documents, which is most of the ask. goja is still worth a look as a tiny script tool next to `http_request` in phase 0.

## 5. Host

Kandang runs on the Mac mini, inside its own isolated OrbStack machine (`orb create --isolated --isolate-network`). Tested 2026-11-19 with a throwaway machine (Debian 13, 3 GB, 2 CPUs), deleted afterwards.

What the isolated machine gives:

- No Mac files. `/Users` and `/mnt/mac` don't exist inside it.
- It can't reach Palui's container (`192.168.117.2:3000` timed out), the host bridge (`host.orb.internal:4893` refused) or other host ports.
- The Mac and Palui's container can reach it (`curl` from both got through to a test server on port 8080), so Palui calls Kandang and nothing calls back.
- It can still reach the internet and the home LAN router. Runs never see that (each run has no network), but a full escape would. A firewall rule inside the machine that drops private ranges closes it.

What it doesn't give: Docker. An isolated machine blocks the device cgroup BPF call, so `docker run` fails with both runc and runsc ("BPF_PROG_ATTACH not supported"). A non-isolated machine runs Docker fine, but it mounts the whole Mac home folder, which is the thing to avoid.

So Kandang drives `runsc` directly, without Docker:

1. The image is built anywhere with Docker (the main OrbStack engine or CI), then unpacked once into a read-only rootfs in the machine (177 MB on disk).
2. Each run gets an OCI bundle: that rootfs read-only, tmpfs `/tmp` and `/work`, uid 10001, no capabilities, `noNewPrivileges`, its own pid, network, ipc, uts and mount namespaces.
3. Kandang creates a cgroup per run (`memory.max` 1 GB, `memory.swap.max` 0, `pids.max`, `cpu.max` one CPU), moves itself in, then execs `runsc --ignore-cgroups --network=none run`. Only the memory, pids and cpu controllers are used; those work in an isolated machine, the device controller doesn't.
4. At the end: `runsc delete --force`, `cgroup.kill`, remove the bundle.

Measured with `runsc` release-20261116.0 (systrap):

| Test | Result |
|---|---|
| Start (`true`) | 25 to 36 ms, 10 MB cgroup peak |
| Light Python job | 636 ms, about 50 MB |
| pandas chart (500k rows, PNG and xlsx) | 3.5 s, 170 to 225 MB |
| 12 MP photo edit | 394 ms, 145 MB |
| bash | Works, `uname` says `4.19.0-gvisor` |
| 2 GB alloc under 1 GB | Killed, exit 137 |
| 300 processes | Stopped at 50 with `ENOMEM` (gVisor's threads count against `pids.max` 128; raise it if 50 is too few) |
| Busy loop, 3 s timeout | Killed at 3,002 ms |
| Network | `Network unreachable` |
| Inside the run | Only the image's own files; uid 10001 |

Against hardened Docker on the main engine: start is about 5 times faster (no Docker in the path), Python work is about 20 to 25% slower (gVisor intercepts syscalls). That trade is fine for chat-sized jobs.

Resources:

| | Mac mini |
|---|---|
| RAM | 16 GB host; OrbStack VM capped at 8 GB, 2 CPUs. Give the machine a 3 GB cap and allow 2 runs at once |
| Disk | Cleaned on 2026-11-19 from 7.1 GB free to 22 GB free (package caches and the Go build cache). The machine itself is about 1.5 GB with the rootfs and `runsc` (18 MB) |
| Arch | arm64 |
| Network path from Palui | Palui's container to the machine's OrbStack address, no internet in between |

## 6. Integration design (sketch)

### 6.1 Shape

```
WhatsApp/Telegram ──> Palui turn ──> sandbox connector ──HTTP+bearer──> Kandang (isolated OrbStack machine)
                         │                │                                  │
                         │                ├─ fileRefs -> bytes               ├─ queue (2), per-run cgroup
                         │                ├─ approvals, audit row            ├─ runsc run (gVisor, network none)
                         │                                                   └─ files in/out of /work
                         └─ send_media <── output files saved as attachments
```

### 6.2 Tool surface

One tool to start, `<connectorId>__run_code`:

- In: `language` (`python` or `bash`), `code`, `files` (attachment refs, `palui:attachment/att_...`), `timeoutSec` (default 60, max 300). No secrets and no network option: Kandang is agnostic.
- Out: `exitCode`, `stdout` and `stderr` (each capped at 32 KB, keeping the head and the tail), `durationMs`, `peakMemoryMb`, `files` (each saved through the attachments store, returned as a ref the model can pass to `send_media`), `truncated` flags.
- Inputs land in `/work/in`, the code writes to `/work/out` (like Anthropic's `$OUTPUT_DIR`), and only top-level files in `/work/out` come back.

Sessions: stateless runs in the MVP. Multi-step work carries data through files (run 1 writes `clean.csv`, run 2 gets it as an input ref). Phase 2 adds an optional session id that keeps one sandbox's `/work` alive for the conversation, killed after 15 min idle or at turn end for shared chats.

### 6.3 Files

- In: refs go through the same swap the MCP connector uses (`attachments.Read`, `MaxRefBytes` 15 MiB), then written into the run's `/work/in` before it starts. Total input cap 50 MB per run.
- Out: Kandang reads `/work/out` after the run, at most 10 files and 15 MiB each, saved with `attachments.Save` (encrypted with `MASTER_KEY`), names cleaned with `basename`. The model then calls `send_media` as it does today, so the existing approval rules for sending saved files in tainted turns still apply.

### 6.4 No secrets

Kandang is agnostic (owner's decision, 2026-11-19). It never receives a credential, and runs have no network. Anything that needs a stored key goes through Palui's `http_request` tool (section 7), where the credential, its domains and its approval policy already live. If a run needs API data, the model calls `http_request` first and passes the result in as a file.

### 6.5 Network

None. Each run gets its own empty network namespace and `runsc --network=none`. An online mode (an allowlist proxy, `pip install`) is an open question for later, not part of the plan.

### 6.6 Approvals and classes

| Tool | Class | Data scope | Taints the turn | Approval |
|---|---|---|---|---|
| `run_code` | `read` (no effect outside a throwaway box; output files only land in Palui's own store) | `chat` | Only if an input file came from an untrusted source | Never, also in jobs and flows (owner's decision, 2026-11-19) |

- Flows: `run_code` is allowed as a tool step.
- `members` and `outside` audiences get it too, which matches its `chat` scope.
- Sending an output file still goes through `send_media` and its existing rules.

### 6.7 Limits and audit

- Per run: gVisor, 1 GB RAM, no swap, 1 CPU, 128 pids, 256 MB `/tmp`, 512 MB `/work`, read-only root, uid 10001, `noNewPrivileges`, no capabilities, no network, timeout 60 s (max 300 s), sandbox and cgroup removed after.
- Per service: 2 runs at once, a queue of 10, then a busy error the model can relay.
- Per turn: at most 8 runs, so a looping model can't burn the box.
- Audit: a `sandbox_runs` table in Palui's DB (Palui is the one that knows the chat, agent and approval): run id, chat, agent, full code, input refs, output refs, exit code, duration, peak memory. Shown on the existing activity page. The service keeps only rotated logs.

### 6.8 Auth between Palui and the service

- A bearer token the owner generates once when setting up the machine, pasted into the connector (stored through `connectors.Upsert`), and kept in a file inside the machine. This is the only secret Kandang holds, and it only guards its own API.
- Kandang listens on the machine's OrbStack address, with no tunnel and no public hostname.
- Kandang accepts only its own JSON schema: no image names, mounts, devices, capabilities or runtime flags from the caller.

## 7. The narrower alternative: `http_request` with stored credentials

A Palui tool: method, URL, headers, body, and an optional credential name. The credential carries its domains and how it's sent (header name and template, or query parameter). GET and HEAD are class `read` but untrusted (taint the turn, like web fetch); POST, PUT, PATCH and DELETE are a second tool with class `write`.

- Covers: "check my ElevenLabs quota" (`GET /v1/user/subscription`), most "look this up in service X" asks, and webhooks.
- Doesn't cover: charts, documents, image edits, CSV and PDF work, anything that needs computation.
- Reuses: `safenet` (SSRF guard), connector secrets, approvals, taint. No new service, image or disk.
- Effort: about 2 days with the dashboard credential editor and tests.

Recommendation: ship it first. It solves the motivating example now, and it's where credentials live for good, since Kandang never holds any. Keep it after the sandbox lands: a typed HTTP call is cheaper, faster and easier to approve than a code run, and the model should prefer it for single calls.

## 8. Phased plan

| Phase | What | Effort |
|---|---|---|
| 0. HTTP | `http_request` and `http_request_write` tools, named credentials with domains and policy, dashboard editor, taint and approval tests | about 2 days |
| 1. Kandang MVP | Isolated OrbStack machine with a private-range firewall; `apps/sandbox` Go service driving `runsc` with per-run OCI bundles and cgroups, queue and limits; Alpine image (170 MB) built with Docker and unpacked as the rootfs; deploy script for the machine; `sandbox` connector with `run_code`, files in and out, `sandbox_runs` audit; changelog | 6 to 8 days |
| 2. Polish | Optional sessions; flows support; a gVisor escape test suite and an update routine for `runsc`; dashboard runs page | 3 to 4 days |
| 3. Extras | Opt-in image variants (full, ffmpeg, LibreOffice, Chromium) chosen per run; MCP endpoint for other agents; decide whether to extract the repo | 3 to 5 days |

## 9. How the numbers were taken

Images built from small Dockerfiles in a scratch folder (base `python:3.12-slim-bookworm`, `pip install --no-cache-dir`, Debian packages for ffmpeg, LibreOffice and Chromium, a non-root user). Peak memory is the container's own `memory.peak` cgroup file, read at the end of each script. Wall times are from the host shell, start to exit, through `docker run --rm`. The small variants used `python:3.12-alpine` and `alpine:3` with `apk` packages. The no-Docker numbers come from a scratch Go program outside the repo. The gVisor numbers come from a throwaway isolated OrbStack machine (Debian 13, Docker 26.1.5 for loading the image, `runsc` from gVisor's apt repo) and a shell script that wrote OCI bundles by hand; peak memory there is the per-run cgroup's `memory.peak` read from outside. All test images and the machine were deleted afterwards. Palui's live container, DB and WhatsApp session weren't touched.

## 10. Sources

- Code execution tool: https://platform.claude.com/docs/en/agents-and-tools/tool-use/code-execution-tool
- Files API: https://platform.claude.com/docs/en/build-with-claude/files
- gVisor platforms: https://gvisor.dev/docs/user_guide/platforms/
- gVisor with Docker: https://gvisor.dev/docs/user_guide/quick_start/docker/
- gVisor install (apt repo): https://gvisor.dev/docs/user_guide/install/
- OrbStack isolated machines: `orb create --help` (`--isolated`, `--isolate-network`)
- gVisor arm64 systrap idle CPU: https://github.com/google/gvisor/issues/13361
- runc 2025 breakouts: https://www.cncf.io/blog/2025/11/28/runc-container-breakout-vulnerabilities-a-technical-overview/
- OrbStack nested virtualization: https://github.com/orbstack/orbstack/issues/1504
- E2B infra: https://github.com/e2b-dev/infra
- Daytona: https://github.com/daytonaio/daytona
- Judge0 cgroup v2: https://github.com/judge0/judge0/issues/543
- Piston: https://github.com/engineer-man/piston
- microsandbox: https://github.com/zerocore-ai/microsandbox
- nsjail: https://github.com/google/nsjail
- Anthropic sandbox-runtime: https://github.com/anthropics/sandbox-runtime
- Pyodide sandbox escapes: https://www.cyera.com/research/sandcastles-not-sandboxes-how-one-architectural-flaw-exposed-seven-products
- Sandbox list: https://github.com/restyler/awesome-sandbox

Third-party numbers (Firecracker boot, microsandbox boot) come from those pages and weren't reproduced here.
