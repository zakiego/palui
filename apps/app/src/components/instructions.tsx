// Standing instructions (PLAN 19.1): the list with an on/off switch per row and
// the sheet that adds, edits and deletes one. Notes and the agent page share them.
import * as React from "react"
import { useRouter } from "@tanstack/react-router"
import { NotebookPenIcon, PencilIcon } from "lucide-react"
import { toast } from "sonner"

import { ClampText } from "@/components/clamp-text"
import { ResponsiveSheet } from "@/components/responsive-sheet"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { StatusText } from "@/components/ui/status-text"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { deleteInstructionFn, saveInstructionFn, setInstructionEnabledFn } from "@/lib/api/notes"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

export type Instruction = { id: string; title: string; body: string; enabled: boolean }

const TITLE_MAX = 80
const BODY_MAX = 1000

// Which instruction the sheet shows. `target` stays set while the sheet closes,
// so it keeps its content through the exit animation.
export function useInstructionSheet() {
  const [target, setTarget] = React.useState<Instruction | "new" | null>(null)
  const [open, setOpen] = React.useState(false)
  return {
    target,
    open,
    edit: (next: Instruction | "new") => {
      setTarget(next)
      setOpen(true)
    },
    close: () => setOpen(false),
    key: target === "new" ? "new" : (target?.id ?? "none"),
  }
}

export function InstructionList({ items, onEdit }: { items: Instruction[]; onEdit: (item: Instruction) => void }) {
  const t = useT()
  if (items.length === 0) {
    return (
      <Empty className="border border-dashed p-6">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <NotebookPenIcon />
          </EmptyMedia>
          <EmptyTitle>{t("notes.emptyInstructions")}</EmptyTitle>
          <EmptyDescription>{t("notes.emptyInstructionsBody")}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <ul className="flex flex-col divide-y">
      {items.map((i) => (
        <InstructionRow key={i.id} item={i} onEdit={() => onEdit(i)} />
      ))}
    </ul>
  )
}

function InstructionRow({ item, onEdit }: { item: Instruction; onEdit: () => void }) {
  const t = useT()
  const router = useRouter()
  const [enabled, setEnabled] = React.useState(item.enabled)
  React.useEffect(() => setEnabled(item.enabled), [item.enabled])

  const toggle = async (next: boolean) => {
    setEnabled(next)
    try {
      await setInstructionEnabledFn({ data: { id: item.id, enabled: next } })
      void router.invalidate()
    } catch (err) {
      setEnabled(!next)
      toast.error(errorMessage(err))
    }
  }

  return (
    <li className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span dir="auto" className="min-w-0 text-left font-medium [overflow-wrap:anywhere]">
            {item.title}
          </span>
          {!enabled && <StatusText tone="secondary">{t("notes.off")}</StatusText>}
        </span>
        <ClampText text={item.body} lines={3} className="text-sm text-muted-foreground [overflow-wrap:anywhere]" />
      </div>
      <span className="flex w-max shrink-0 items-center gap-1">
        <span className="flex size-11 items-center justify-center">
          <Switch checked={enabled} onCheckedChange={(v) => void toggle(v)} aria-label={t("notes.toggleInstruction", { name: item.title })} className="after:-inset-x-1.5 after:-inset-y-[13px]" />
        </span>
        <Button variant="ghost" size="icon-sm" onClick={onEdit} aria-label={t("notes.editInstruction", { name: item.title })}>
          <PencilIcon aria-hidden="true" />
        </Button>
      </span>
    </li>
  )
}

export function InstructionSheet({ open, target, agentId, onClose }: { open: boolean; target: Instruction | "new" | null; agentId: string; onClose: () => void }) {
  const t = useT()
  const router = useRouter()
  const item = target && target !== "new" ? target : null
  const [title, setTitle] = React.useState(item?.title ?? "")
  const [body, setBody] = React.useState(item?.body ?? "")
  const [errors, setErrors] = React.useState<{ title?: string; body?: string }>({})
  const [busy, setBusy] = React.useState<null | "save" | "delete">(null)

  const submit = async () => {
    const next: typeof errors = {}
    if (!title.trim()) next.title = t("notes.err.title")
    if (!body.trim()) next.body = t("notes.err.body")
    setErrors(next)
    if (Object.keys(next).length) return
    setBusy("save")
    try {
      await saveInstructionFn({ data: { id: item?.id, agentId, title: title.trim(), body: body.trim() } })
      toast.success(t("notes.instructionSaved"))
      onClose()
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const remove = async () => {
    if (!item) return
    setBusy("delete")
    try {
      await deleteInstructionFn({ data: { id: item.id } })
      toast.success(t("notes.instructionDeleted", { name: item.title }))
      onClose()
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <ResponsiveSheet
      open={open && target !== null}
      onOpenChange={(o) => !o && onClose()}
      title={item ? item.title : t("notes.newInstruction")}
      footer={
        <div className="flex w-full gap-2">
          <Button variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button className="ms-auto" disabled={busy !== null} onClick={() => void submit()}>
            {busy === "save" && <Spinner data-icon="inline-start" />}
            {t("common.save")}
          </Button>
        </div>
      }
    >
      <FieldGroup>
        <Field data-invalid={!!errors.title}>
          <FieldLabel htmlFor="ins-title">{t("notes.field.title")}</FieldLabel>
          <Input id="ins-title" value={title} maxLength={TITLE_MAX} aria-invalid={!!errors.title} onChange={(e) => setTitle(e.target.value)} />
          {errors.title && <FieldError>{errors.title}</FieldError>}
        </Field>
        <Field data-invalid={!!errors.body}>
          <FieldLabel htmlFor="ins-body">{t("notes.field.body")}</FieldLabel>
          <Textarea id="ins-body" rows={8} maxLength={BODY_MAX} value={body} aria-invalid={!!errors.body} onChange={(e) => setBody(e.target.value)} />
          <FieldDescription className="tabular-nums">{t("notes.chars", { n: body.length.toLocaleString(), max: BODY_MAX.toLocaleString() })}</FieldDescription>
          {errors.body && <FieldError>{errors.body}</FieldError>}
        </Field>
      </FieldGroup>
      {item && (
        <AlertDialog>
          <AlertDialogTrigger render={<Button variant="destructive" className="self-start" disabled={busy !== null} />}>
            {busy === "delete" && <Spinner data-icon="inline-start" />}
            {t("notes.deleteInstruction")}
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle className="[overflow-wrap:anywhere]">{t("notes.deleteInstructionTitle", { name: item.title })}</AlertDialogTitle>
              <AlertDialogDescription>{t("notes.deleteInstructionBody")}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
              <AlertDialogAction variant="danger" onClick={() => void remove()}>
                {t("common.delete")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </ResponsiveSheet>
  )
}
