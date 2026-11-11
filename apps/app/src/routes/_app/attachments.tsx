// Attachments: files the agent saved from chats (KTP, receipts, tickets).
// Read-only list with download and delete; saving happens in chat.
import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { DownloadIcon, FileTextIcon, ImageIcon, PaperclipIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
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
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { deleteAttachmentFn, getAttachments } from "@/lib/fns/attachments"
import { errorMessage, inZone } from "@/lib/format"
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
                <TableHead className="w-24">
                  <span className="sr-only">{t("table.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.attachments.map((a) => (
                <TableRow key={a.id} className={cn(a.id === id && "bg-muted")} aria-current={a.id === id ? "true" : undefined}>
                  <TableCell className="max-w-0 whitespace-normal">
                    <span className="flex min-w-0 items-center gap-2">
                      {a.kind === "image" ? <ImageIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" /> : <FileTextIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />}
                      <span className="truncate font-medium" dir="auto" title={a.title}>
                        {a.title}
                      </span>
                    </span>
                    {a.description && <span className="mt-1 line-clamp-2 text-sm break-words text-muted-foreground">{a.description}</span>}
                    <span className="mt-1 block truncate font-mono text-caption text-muted-foreground">{a.filename ?? a.mimeType}</span>
                    {/* Chat, size and date fold in under the file when the page is narrow. */}
                    <span className="mt-0.5 block truncate text-caption text-muted-foreground @3xl/main:hidden">
                      <span className="font-mono tabular-nums @2xl/main:hidden">
                        {size(a.size)} · {inZone(a.createdAt, data.timezone)} ·{" "}
                      </span>
                      {a.chat}
                    </span>
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

function RowActions({ row }: { row: Row }) {
  const t = useT()
  const router = useRouter()
  const [confirm, setConfirm] = React.useState(false)
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
      {/* /api lives at the root, outside the /app basepath. */}
      <Button variant="ghost" size="icon-sm" aria-label={`${t("attachments.download")} ${row.title}`} render={<a href={`/api/attachments/${row.id}`} download />}>
        <DownloadIcon aria-hidden="true" />
      </Button>
      <Button variant="ghost" size="icon-sm" aria-label={`${t("common.delete")} ${row.title}`} onClick={() => setConfirm(true)}>
        <Trash2Icon aria-hidden="true" />
      </Button>
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
      <PageHeaderSkeleton title="w-36" description="w-0" wraps={false} />
      <TableSkeleton
        rows={4}
        columns={[
          { head: "w-10", sub: ["", "@3xl/main:hidden"] },
          { className: "hidden w-40 @3xl/main:table-cell", head: "w-10", short: "w-24" },
          { className: "hidden w-20 @2xl/main:table-cell", head: "w-8", text: "font-mono text-caption", short: "w-10" },
          { className: "hidden w-36 @2xl/main:table-cell", head: "w-12", text: "font-mono text-caption", short: "w-28" },
          { className: "w-24", controls: ["size-7", "size-7"] },
        ]}
      />
    </PagePending>
  )
}
