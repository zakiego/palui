// Attachments: files the agent saved from chats (KTP, receipts, tickets).
// Preview, share as a temporary link, download and delete; saving happens in chat.
import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { DownloadIcon, ExternalLinkIcon, EyeIcon, FileTextIcon, ImageIcon, Link2Icon, PaperclipIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { CopyButton } from "@/components/copy-button"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, TableSkeleton } from "@/components/page-pending"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { SimpleSelect } from "@/components/simple-select"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { createAttachmentShareFn, deleteAttachmentFn, getAttachments, getAttachmentShares, revokeAttachmentShareFn } from "@/lib/fns/attachments"
import { ago, errorMessage, inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/attachments")({
  // ?id= comes from the link the agent shares in chat; that row is highlighted.
  validateSearch: z.object({ id: z.string().max(64).optional() }),
  loader: () => getAttachments(),
  head: () => ({ meta: [{ title: "Attachments · Palui" }] }),
  component: AttachmentsPage,
  pendingComponent: AttachmentsSkeleton,
  errorComponent: AppError,
})

type Row = Awaited<ReturnType<typeof getAttachments>>["attachments"][number]

function size(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

function AttachmentsPage() {
  const data = Route.useLoaderData()
  const { id } = Route.useSearch()
  const t = useT()
  return (
    <>
      <PageHeader title={t("attachments.title")} />
      {data.attachments.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <PaperclipIcon />
            </EmptyMedia>
            <EmptyTitle>{t("attachments.empty")}</EmptyTitle>
            <EmptyDescription>{t("attachments.emptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button variant="outline" size="sm" render={<Link to="/chats" />}>
              {t("command.openChat")}
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("table.file")}</TableHead>
                <TableHead className="hidden w-40 @3xl/main:table-cell">{t("table.chat")}</TableHead>
                <TableHead className="hidden w-20 text-end @2xl/main:table-cell">{t("table.size")}</TableHead>
                <TableHead className="hidden w-36 @2xl/main:table-cell">{t("table.saved")}</TableHead>
                <TableHead className="w-20 @2xl/main:w-36">
                  <span className="sr-only">{t("table.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.attachments.map((a) => (
                <TableRow key={a.id} className={cn(a.id === id && "bg-muted")} aria-current={a.id === id ? "true" : undefined}>
                  <TableCell className="max-w-0 whitespace-normal">
                    <FileCell row={a} timezone={data.timezone} />
                  </TableCell>
                  <TableCell className="hidden max-w-0 truncate @3xl/main:table-cell" title={a.chat}>
                    {a.chat}
                  </TableCell>
                  <TableCell className="hidden text-end font-mono text-caption tabular-nums @2xl/main:table-cell">{size(a.size)}</TableCell>
                  <TableCell className="hidden font-mono text-caption tabular-nums @2xl/main:table-cell">{inZone(a.createdAt, data.timezone)}</TableCell>
                  <TableCell className="text-end">
                    <RowActions row={a} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </>
  )
}

// Opens and previews through the signed-in route; /api lives at the root, outside the /app basepath.
const fileUrl = (id: string, inline = false) => `/api/attachments/${id}${inline ? "?inline=1" : ""}`

// Thumbnail (or type icon), title and details. The thumbnail and the title both
// open the preview; so does the agent's ?id= link.
function FileCell({ row, timezone }: { row: Row; timezone: string }) {
  const t = useT()
  const { id } = Route.useSearch()
  const [open, setOpen] = React.useState(false)
  const [broken, setBroken] = React.useState(false)
  // An image that failed before hydration never fires onError, so check it once mounted.
  const thumb = React.useCallback((img: HTMLImageElement | null) => {
    if (img?.complete && img.naturalWidth === 0) setBroken(true)
  }, [])
  React.useEffect(() => {
    if (id === row.id) setOpen(true)
  }, [id, row.id])
  const Icon = row.kind === "image" ? ImageIcon : FileTextIcon
  return (
    <div className="flex min-w-0 items-start gap-3">
      <button
        type="button"
        tabIndex={-1}
        aria-hidden="true"
        className="relative flex size-12 shrink-0 items-center justify-center overflow-hidden rounded-md border bg-muted text-muted-foreground transition-opacity hover:opacity-80"
        onClick={() => setOpen(true)}
      >
        {row.preview === "image" && !broken ? (
          <img ref={thumb} src={fileUrl(row.id, true)} alt="" loading="lazy" decoding="async" className="size-full object-cover" onError={() => setBroken(true)} />
        ) : (
          <Icon className="size-5" />
        )}
      </button>
      <div className="min-w-0 flex-1">
        <button
          type="button"
          className="block max-w-full truncate text-start font-medium underline-offset-4 hover:underline focus-visible:underline focus-visible:outline-none"
          dir="auto"
          title={row.title}
          onClick={() => setOpen(true)}
        >
          {row.title}
        </button>
        {row.description && <span className="mt-1 line-clamp-2 text-sm break-words text-muted-foreground">{row.description}</span>}
        <span className="mt-1 block truncate font-mono text-caption text-muted-foreground">{row.filename ?? row.mimeType}</span>
        {row.shares > 0 && (
          <span className="mt-1 flex items-center gap-1 text-caption text-muted-foreground">
            <Link2Icon aria-hidden="true" className="size-3.5 shrink-0 text-primary" />
            {t("attachments.liveLinks", { n: row.shares.toLocaleString() })}
          </span>
        )}
        {/* Chat, size and date fold in under the file when the page is narrow. */}
        <span className="mt-0.5 block truncate text-caption text-muted-foreground @3xl/main:hidden">
          <span className="font-mono tabular-nums @2xl/main:hidden">
            {size(row.size)} · {inZone(row.createdAt, timezone)} ·{" "}
          </span>
          {row.chat}
        </span>
      </div>
      <PreviewDialog row={row} open={open} onOpenChange={setOpen} />
    </div>
  )
}

function PreviewDialog({ row, open, onOpenChange }: { row: Row; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT()
  const [share, setShare] = React.useState(false)
  const src = fileUrl(row.id, true)
  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="flex max-h-[92dvh] flex-col sm:max-w-3xl">
          <DialogHeader className="min-w-0 pe-8">
            <DialogTitle className="truncate" dir="auto">
              {row.title}
            </DialogTitle>
            {row.description && <DialogDescription className="line-clamp-3 break-words">{row.description}</DialogDescription>}
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-auto rounded-lg bg-muted">
            {open && row.preview === "image" && <img src={src} alt={row.title} className="mx-auto block max-h-[65dvh] w-auto max-w-full object-contain" />}
            {open && (row.preview === "pdf" || row.preview === "text") && (
              <iframe src={src} title={row.title} className={cn("block w-full border-0", row.preview === "pdf" ? "h-[65dvh]" : "h-[50dvh] bg-background")} />
            )}
            {!row.preview && (
              <Empty className="py-12">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <FileTextIcon />
                  </EmptyMedia>
                  <EmptyTitle>{t("attachments.noPreview")}</EmptyTitle>
                  <EmptyDescription>{t("attachments.noPreviewBody")}</EmptyDescription>
                </EmptyHeader>
              </Empty>
            )}
          </div>
          <DialogFooter className="flex-row flex-wrap justify-end gap-2">
            {row.preview && (
              <Button variant="ghost" size="sm" render={<a href={src} target="_blank" rel="noopener noreferrer" />}>
                <ExternalLinkIcon aria-hidden="true" />
                {t("attachments.openTab")}
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={() => setShare(true)}>
              <Link2Icon aria-hidden="true" />
              {t("attachments.share")}
            </Button>
            <Button size="sm" render={<a href={fileUrl(row.id)} download />}>
              <DownloadIcon aria-hidden="true" />
              {t("attachments.download")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ShareDialog row={row} open={share} onOpenChange={setShare} />
    </>
  )
}

type Share = Awaited<ReturnType<typeof getAttachmentShares>>["shares"][number]

function ShareDialog({ row, open, onOpenChange }: { row: Row; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT()
  const router = useRouter()
  const [visibility, setVisibility] = React.useState<"public" | "private">("public")
  const [duration, setDuration] = React.useState<"1h" | "24h" | "7d">("24h")
  const [busy, setBusy] = React.useState(false)
  const [created, setCreated] = React.useState<string>()
  const [shares, setShares] = React.useState<Share[]>()

  const load = React.useCallback(async () => {
    try {
      setShares((await getAttachmentShares({ data: { id: row.id } })).shares)
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }, [row.id])

  React.useEffect(() => {
    if (!open) return
    setCreated(undefined)
    void load()
  }, [open, load])

  const create = async () => {
    setBusy(true)
    try {
      const res = await createAttachmentShareFn({ data: { id: row.id, visibility, duration } })
      setCreated(res.url)
      void load()
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const revoke = async (shareId: string) => {
    try {
      await revokeAttachmentShareFn({ data: { shareId } })
      toast.success(t("attachments.linkOff"))
      void load()
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader className="min-w-0 pe-8">
          <DialogTitle>{t("attachments.shareTitle")}</DialogTitle>
          <DialogDescription>
            <span className="block truncate font-medium text-foreground" dir="auto">
              {row.title}
            </span>
            {t("attachments.shareBody")}
          </DialogDescription>
        </DialogHeader>
        {created ? (
          <Field>
            <FieldLabel>URL</FieldLabel>
            <div className="flex min-w-0 items-center gap-2">
              <code className="min-w-0 flex-1 rounded-md bg-muted p-2 font-mono text-caption break-all">{created}</code>
              <CopyButton text={created} label={t("common.copy")} />
            </div>
            <p className="text-caption text-muted-foreground">{t("attachments.linkReady")}</p>
          </Field>
        ) : (
          <form
            id={`share-${row.id}`}
            onSubmit={(e) => {
              e.preventDefault()
              void create()
            }}
          >
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor={`share-who-${row.id}`}>{t("attachments.who")}</FieldLabel>
                <SimpleSelect
                  id={`share-who-${row.id}`}
                  className="w-full"
                  value={visibility}
                  onChange={(v) => setVisibility(v === "private" ? "private" : "public")}
                  options={[
                    { value: "public", label: t("attachments.public") },
                    { value: "private", label: t("attachments.private") },
                  ]}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor={`share-for-${row.id}`}>{t("attachments.expiresAfter")}</FieldLabel>
                <SimpleSelect
                  id={`share-for-${row.id}`}
                  className="w-full"
                  value={duration}
                  onChange={(v) => setDuration(v === "1h" || v === "7d" ? v : "24h")}
                  options={[
                    { value: "1h", label: t("attachments.d1h") },
                    { value: "24h", label: t("attachments.d24h") },
                    { value: "7d", label: t("attachments.d7d") },
                  ]}
                />
              </Field>
            </FieldGroup>
          </form>
        )}

        <div className="flex flex-col gap-2">
          <h3 className="text-sm font-medium">{t("attachments.activeLinks")}</h3>
          {shares && shares.length === 0 && <p className="text-sm text-muted-foreground">{t("attachments.noLinks")}</p>}
          {shares && shares.length > 0 && (
            <ul className="-mx-1 flex max-h-56 flex-col gap-2 overflow-y-auto px-1">
              {shares.map((s) => (
                <li key={s.id} className="flex min-w-0 items-center gap-3 rounded-lg border px-3 py-2">
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium">{s.visibility === "public" ? t("attachments.publicShort") : t("attachments.privateShort")}</span>
                    <span className="block truncate text-caption text-muted-foreground">
                      {t("attachments.expiresIn", { when: ago(s.expiresAt) })} · {t("attachments.opened", { n: s.openCount.toLocaleString() })}
                      {s.createdBy !== "owner" && ` · ${t("attachments.byAgent")}`}
                    </span>
                  </span>
                  <Button variant="ghost" size="sm" onClick={() => void revoke(s.id)}>
                    {t("attachments.turnOff")}
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <DialogFooter>
          {created ? (
            <Button variant="outline" onClick={() => setCreated(undefined)}>
              {t("attachments.newLink")}
            </Button>
          ) : (
            <Button type="submit" form={`share-${row.id}`} disabled={busy}>
              {t("attachments.createLink")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function RowActions({ row }: { row: Row }) {
  const t = useT()
  const router = useRouter()
  const [confirm, setConfirm] = React.useState(false)
  const [preview, setPreview] = React.useState(false)
  const [share, setShare] = React.useState(false)
  const remove = async () => {
    try {
      await deleteAttachmentFn({ data: { id: row.id } })
      toast.success(t("attachments.deleted"))
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  return (
    <span className="flex justify-end gap-1">
      {/* On a narrow page the thumbnail and title open the preview, which has Share. */}
      <Button variant="ghost" size="icon-sm" className="hidden @2xl/main:inline-flex" aria-label={`${t("attachments.preview")} ${row.title}`} onClick={() => setPreview(true)}>
        <EyeIcon aria-hidden="true" />
      </Button>
      <Button variant="ghost" size="icon-sm" className="hidden @2xl/main:inline-flex" aria-label={`${t("attachments.share")} ${row.title}`} onClick={() => setShare(true)}>
        <Link2Icon aria-hidden="true" />
      </Button>
      <Button variant="ghost" size="icon-sm" aria-label={`${t("attachments.download")} ${row.title}`} render={<a href={fileUrl(row.id)} download />}>
        <DownloadIcon aria-hidden="true" />
      </Button>
      <Button variant="ghost" size="icon-sm" aria-label={`${t("common.delete")} ${row.title}`} onClick={() => setConfirm(true)}>
        <Trash2Icon aria-hidden="true" />
      </Button>
      <PreviewDialog row={row} open={preview} onOpenChange={setPreview} />
      <ShareDialog row={row} open={share} onOpenChange={setShare} />
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("attachments.deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("attachments.deleteBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.keep")}</AlertDialogCancel>
            <AlertDialogAction variant="danger" onClick={() => void remove()}>
              {t("common.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </span>
  )
}

function AttachmentsSkeleton() {
  return (
    <PagePending page="nav.attachments">
      <PageHeaderSkeleton title="w-36" wraps={false} />
      <TableSkeleton
        rows={4}
        columns={[
          { head: "w-10", sub: ["", "@3xl/main:hidden"] },
          { className: "hidden w-40 @3xl/main:table-cell", head: "w-10", short: "w-24" },
          { className: "hidden w-20 @2xl/main:table-cell", head: "w-8", text: "font-mono text-caption", short: "w-10" },
          { className: "hidden w-36 @2xl/main:table-cell", head: "w-12", text: "font-mono text-caption", short: "w-28" },
          { className: "w-20 @2xl/main:w-36", controls: ["size-7", "size-7"] },
        ]}
      />
    </PagePending>
  )
}
