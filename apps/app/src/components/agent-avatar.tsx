// An agent's avatar (PLAN 19.7): its uploaded photo, else its icon on its color,
// else Palui's mark for the default agent and a plain bot for the rest. The
// names mirror AGENT_ICONS and AGENT_COLORS in @palui/core/agents (a test keeps
// them in step); this file can't import that module, it pulls in the database.
import {
  BookOpenIcon,
  BotIcon,
  BriefcaseIcon,
  CalculatorIcon,
  CameraIcon,
  ChefHatIcon,
  CodeIcon,
  CoffeeIcon,
  DumbbellIcon,
  GraduationCapIcon,
  HeadsetIcon,
  HeartIcon,
  HouseIcon,
  LeafIcon,
  MoonIcon,
  MusicIcon,
  PlaneIcon,
  ScaleIcon,
  SmileIcon,
  SparklesIcon,
  StarIcon,
  StethoscopeIcon,
  StoreIcon,
  WrenchIcon,
  type LucideIcon,
} from "lucide-react"
import { PaluiMark } from "@/components/palui-mark"
import { cn } from "@/lib/utils"

export const AVATAR_ICONS: Record<string, LucideIcon> = {
  bot: BotIcon,
  sparkles: SparklesIcon,
  smile: SmileIcon,
  heart: HeartIcon,
  star: StarIcon,
  "chef-hat": ChefHatIcon,
  coffee: CoffeeIcon,
  briefcase: BriefcaseIcon,
  headset: HeadsetIcon,
  store: StoreIcon,
  home: HouseIcon,
  "book-open": BookOpenIcon,
  "graduation-cap": GraduationCapIcon,
  code: CodeIcon,
  wrench: WrenchIcon,
  calculator: CalculatorIcon,
  scale: ScaleIcon,
  stethoscope: StethoscopeIcon,
  dumbbell: DumbbellIcon,
  leaf: LeafIcon,
  music: MusicIcon,
  camera: CameraIcon,
  plane: PlaneIcon,
  moon: MoonIcon,
}

export const AVATAR_COLORS = ["zinc", "red", "orange", "amber", "green", "teal", "blue", "violet", "pink"] as const

export type AvatarFields = { isDefault?: boolean; avatarImage?: string | null; avatarIcon?: string | null; avatarColor?: string | null }

const SIZE = {
  xs: "size-5 rounded [&_svg]:size-3",
  sm: "size-8 rounded-md [&_svg]:size-4",
  md: "size-10 rounded-lg [&_svg]:size-5",
  lg: "size-16 rounded-xl [&_svg]:size-8",
} as const

export function AgentAvatar({ agent, size = "md", className }: { agent: AvatarFields; size?: keyof typeof SIZE; className?: string }) {
  const box = cn("flex shrink-0 items-center justify-center overflow-hidden", SIZE[size], className)
  if (agent.avatarImage) return <img src={agent.avatarImage} alt="" aria-hidden="true" className={cn(box, "object-cover")} />
  if (agent.isDefault && !agent.avatarIcon) return <PaluiMark className={box} />
  const Icon = (agent.avatarIcon && AVATAR_ICONS[agent.avatarIcon]) || BotIcon
  return (
    <span aria-hidden="true" data-avatar-tone={agent.avatarColor ?? "zinc"} className={cn(box, "bg-(--avatar-bg) text-(--avatar-fg)")}>
      <Icon />
    </span>
  )
}

// A photo the owner picked, cropped to a centered square and re-encoded small
// (256 px WebP), so what's stored is a few KB whatever the camera produced.
export async function resizeAvatar(file: File, px = 256): Promise<string> {
  const bitmap = await createImageBitmap(file)
  try {
    const side = Math.min(bitmap.width, bitmap.height)
    const canvas = document.createElement("canvas")
    canvas.width = px
    canvas.height = px
    const ctx = canvas.getContext("2d")
    if (!ctx) throw new Error("This browser can't resize images.")
    ctx.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, px, px)
    const webp = canvas.toDataURL("image/webp", 0.85)
    // Safari before 17 can't encode WebP and silently returns PNG; JPEG is smaller then.
    return webp.startsWith("data:image/webp") ? webp : canvas.toDataURL("image/jpeg", 0.85)
  } finally {
    bitmap.close()
  }
}
