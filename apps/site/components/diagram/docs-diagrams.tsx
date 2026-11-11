import {
  ArchiveIcon,
  BellRingIcon,
  BrainIcon,
  Cable,
  CircleCheckIcon,
  CircleDashedIcon,
  CircleOffIcon,
  CloudUploadIcon,
  ClockIcon,
  Container,
  CpuIcon,
  DatabaseIcon,
  Globe,
  HardDrive,
  HourglassIcon,
  InboxIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  ListOrderedIcon,
  LoaderIcon,
  LockIcon,
  MessageCircleIcon,
  MessageSquareReplyIcon,
  PanelsTopLeft,
  PauseIcon,
  PlayIcon,
  PlugIcon,
  RouteIcon,
  ScaleIcon,
  SendIcon,
  Server,
  ShieldAlertIcon,
  SparklesIcon,
  TriangleAlertIcon,
  WrenchIcon,
} from "lucide-react"
import type { ReactNode } from "react"
import { Arrow, Diagram, Fork, Group, Join, Node } from "./primitives"

// The docs diagrams, drawn as responsive graphs (TASTE.md: never ASCII). Labels
// stay short; the prose around each diagram carries the numbers and reasons.
// The MDX children are the text description, read by screen readers and kept
// in the markdown export.

type Props = { children: ReactNode }

const mono = (text: string) => (
  <span translate="no" className="font-mono">
    {text}
  </span>
)

export function ArchitectureDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Join>
        {[
          <Node key="wa" stack icon={MessageCircleIcon} title="WhatsApp" />,
          <Node key="tg" stack icon={SendIcon} title="Telegram" />,
          <Node key="web" stack icon={LayoutDashboardIcon} title="Web chat" />,
        ]}
      </Join>
      <Group icon={Server} label="The Docker host (one image, two services)">
        <Node icon={Container} title="runtime" detail="router, turns, scheduler, outbox, Saklar" />
        <Arrow label="control API, compose network only" />
        <Node icon={Container} title="app" detail="dashboard /app, REST /api, /health, migrations" />
        <Arrow />
        <Node icon={HardDrive} title="Data volume" detail="/data: palui.db, wa-auth/, backups/" />
      </Group>
      <Fork labels={["models", "memory", "tools"]}>
        <Node stack icon={CpuIcon} title="CLIProxyAPI, ai-gateway" />
        <Node stack icon={BrainIcon} title="recall" />
        <Node stack icon={PlugIcon} title="MCP servers" />
      </Fork>
    </Diagram>
  )
}

export function MessageFlowDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Node icon={MessageCircleIcon} title="Channel" detail="Telegram, WhatsApp, web chat" />
      <Arrow label="written before the handler returns" />
      <Node icon={InboxIcon} title="Inbox" detail="messages, status pending" />
      <Arrow />
      <Node icon={RouteIcon} title="Router" detail="owner check, commands, approval answers" />
      <Arrow />
      <Node icon={ListOrderedIcon} title="Per-chat queue" detail="one turn at a time" />
      <Arrow />
      <Node icon={SparklesIcon} title="Turn" detail="context, model loop, tools" />
      <Arrow />
      <Node icon={ArchiveIcon} title="Outbox" detail="claim, send, retry" />
      <Arrow label="sent only after the channel acks" />
      <Node icon={MessageSquareReplyIcon} title="Reply in the same chat" />
    </Diagram>
  )
}

export function SaklarStatesDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Node icon={CircleDashedIcon} title="Not set up" />
      <Arrow label="connect" />
      <Node icon={LoaderIcon} title="Connecting" />
      <Fork labels={["ok", "fails"]}>
        <Node stack icon={CircleCheckIcon} title="On" />
        <Node stack icon={TriangleAlertIcon} title="Error" detail="backoff 5 s to 10 min" />
      </Fork>
      <Arrow label="pause (keeps credentials)" />
      <Node icon={PauseIcon} title="Paused" />
      <Arrow label="resume" />
      <Node icon={PlayIcon} title="Connecting again" />
      <Arrow label="remove, from any state (asks first)" />
      <Node icon={CircleOffIcon} title="Not set up" detail="credentials deleted" optional />
    </Diagram>
  )
}

export function ApprovalDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Node icon={WrenchIcon} title="The model calls a tool" detail="destructive, or write in a tainted turn or a job" />
      <Arrow label="needs approval" />
      <Node icon={HourglassIcon} title="Turn ends, request saved" detail="pending_approvals" />
      <Arrow />
      <Node icon={BellRingIcon} title="You get a prompt" detail="buttons on Telegram, ya / batal on WhatsApp" />
      <Fork labels={["you answer", "10 minutes pass"]}>
        <Node stack icon={PlayIcon} title="Turn resumes" detail="approved or denied" />
        <Node stack icon={ClockIcon} title="Resumes as denied" />
      </Fork>
    </Diagram>
  )
}

export function EdgeRoutingDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Node icon={Globe} title="Request" detail="palui.zakiego.com/*" />
      <Arrow />
      <Node icon={RouteIcon} title="Cloudflare Worker" detail="apps/edge" />
      <Fork labels={[mono("/app /api /health"), "everything else"]}>
        <Node stack icon={Cable} title="Your tunnel" />
        <Node stack icon={PanelsTopLeft} title="Cloudflare Pages" detail="this site" />
      </Fork>
      <div className="flex flex-col @2xl:w-[calc((100%-0.75rem)/2)]">
        <Arrow />
        <Node icon={Container} title="app" detail="127.0.0.1 on the Docker host" />
      </div>
    </Diagram>
  )
}

export function BackupDiagram({ children }: Props) {
  return (
    <Diagram description={children}>
      <Node icon={ClockIcon} title="Nightly job" detail="03:30, launchd" />
      <Fork labels={["app CLI backup", "tar, streamed"]}>
        <Node stack icon={DatabaseIcon} title="DB snapshot" detail="palui.db" />
        <Node stack icon={KeyRoundIcon} title="WhatsApp auth" detail="/data/wa-auth" />
      </Fork>
      <Join>
        {[
          <Node key="db" stack icon={LockIcon} title="age encrypt" detail=".db.gz.age" />,
          <Node key="wa" stack icon={LockIcon} title="age encrypt" detail=".tar.gz.age" />,
        ]}
      </Join>
      <Node icon={CloudUploadIcon} title="Upload to R2" detail="palui-backups" />
      <Arrow />
      <Node icon={ScaleIcon} title="Size check" detail="local bytes = R2 bytes" />
      <Arrow label="weekly or after changes" />
      <Node icon={ShieldAlertIcon} title="Restore drill" detail="scripts/restore-drill.sh" optional />
    </Diagram>
  )
}
