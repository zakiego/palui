// Settings > Security: who may call Palui's own MCP server. The URL goes into
// Claude (OAuth, consent on /oauth/consent); a personal token is for agents
// that run in a terminal and can send a header. Tokens are shown once.
import * as React from "react"
import { useRouter } from "@tanstack/react-router"
import { BotIcon, KeyRoundIcon, PlugIcon, PlusIcon, TrashIcon } from "lucide-react"
import { toast } from "sonner"

import { CopyButton } from "@/components/copy-button"
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
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Spinner } from "@/components/ui/spinner"
import { createMcpToken, revokeMcpAccess, type McpAccess, type McpGrant } from "@/lib/api/mcp"
import { ago, errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

const MAX_NAME = 60

// The one-line Claude Code setup for a new token.
export function claudeCodeCommand(url: string, token: string) {
  return `claude mcp add --transport http palui ${url} --header "Authorization: Bearer ${token}"`
}

function hostOf(uri: string | null) {
  if (!uri) return null
  try {
    return new URL(uri).host
  } catch {
    return null
  }
}

export function McpAccessCard({ access }: { access: McpAccess }) {
  const router = useRouter()
  const t = useT()
  const [open, setOpen] = React.useState(false)
  const [name, setName] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [created, setCreated] = React.useState<string | null>(null)

  const create = async () => {
    setBusy(true)
    try {
      const res = await createMcpToken({ data: { name: name.trim() } })
      setCreated(res.token)
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  const revoke = async (g: McpGrant) => {
    try {
      await revokeMcpAccess({ data: { id: g.id } })
      toast.success(t("settings.mcp.revoked"))
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  const startNew = () => {
    setCreated(null)
    setName("")
    setOpen(true)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.mcp.title")}</CardTitle>
        <CardDescription>{t("settings.mcp.body")}</CardDescription>
        <CardAction>
          <Button variant="outline" size="sm" onClick={startNew}>
            <PlusIcon data-icon="inline-start" />
            {t("settings.mcp.new")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <Field>
          <FieldLabel>{t("settings.mcp.url")}</FieldLabel>
          <div className="flex min-w-0 items-center gap-2">
            <code className="min-w-0 flex-1 rounded-md bg-muted px-2 py-1.5 font-mono text-caption break-all">{access.url}</code>
            <CopyButton text={access.url} label={t("settings.mcp.copyUrl")} />
          </div>
        </Field>
        {access.grants.length === 0 ? (
          <Empty className="border border-dashed p-6">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <BotIcon />
              </EmptyMedia>
              <EmptyTitle>{t("settings.mcp.empty")}</EmptyTitle>
              <EmptyDescription>{t("settings.mcp.emptyBody")}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ItemGroup className="gap-0">
            {access.grants.map((g) => (
              <GrantRow key={g.id} grant={g} onRevoke={() => void revoke(g)} />
            ))}
          </ItemGroup>
        )}
      </CardContent>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{created ? t("settings.mcp.createdTitle") : t("settings.mcp.new")}</DialogTitle>
            {created && <DialogDescription>{t("settings.mcp.createdBody")}</DialogDescription>}
          </DialogHeader>
          {created ? (
            <FieldGroup>
              <Field>
                <FieldLabel>Token</FieldLabel>
                <div className="flex min-w-0 items-center gap-2">
                  <code className="min-w-0 flex-1 rounded-md bg-muted p-2 font-mono text-caption break-all">{created}</code>
                  <CopyButton text={created} />
                </div>
              </Field>
              <Field>
                <FieldLabel>{t("settings.mcp.claudeCode")}</FieldLabel>
                <div className="flex min-w-0 items-start gap-2">
                  <code className="min-w-0 flex-1 rounded-md bg-muted p-2 font-mono text-caption break-all">{claudeCodeCommand(access.url, created)}</code>
                  <CopyButton text={claudeCodeCommand(access.url, created)} />
                </div>
              </Field>
            </FieldGroup>
          ) : (
            <form
              id="new-mcp-token"
              onSubmit={(e) => {
                e.preventDefault()
                if (name.trim()) void create()
              }}
            >
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="mcp-token-name">{t("settings.mcp.name")}</FieldLabel>
                  <Input id="mcp-token-name" autoFocus maxLength={MAX_NAME} value={name} onChange={(e) => setName(e.target.value)} placeholder="Claude Code on my laptop" />
                  <FieldDescription>{t("settings.mcp.nameHelp")}</FieldDescription>
                </Field>
              </FieldGroup>
            </form>
          )}
          <DialogFooter>
            {created ? (
              <Button onClick={() => setOpen(false)}>{t("settings.mcp.done")}</Button>
            ) : (
              <>
                <Button variant="outline" onClick={() => setOpen(false)}>
                  {t("common.cancel")}
                </Button>
                <Button type="submit" form="new-mcp-token" disabled={busy || !name.trim()}>
                  {busy && <Spinner data-icon="inline-start" />}
                  {t("settings.mcp.create")}
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}

function GrantRow({ grant: g, onRevoke }: { grant: McpGrant; onRevoke: () => void }) {
  const t = useT()
  const name = g.name.trim() || t("settings.mcp.app")
  const host = hostOf(g.clientUri)
  const what = g.kind === "token" ? t("settings.mcp.token", { hint: `…${g.tokenHint ?? ""}` }) : host ? `${t("settings.mcp.app")} · ${host}` : t("settings.mcp.app")
  const used = g.lastUsedAt ? t("settings.mcp.used", { time: ago(g.lastUsedAt) }) : t("settings.mcp.neverUsed")
  return (
    <Item size="sm" className="px-0">
      <ItemMedia variant="icon">{g.kind === "token" ? <KeyRoundIcon aria-hidden="true" /> : <PlugIcon aria-hidden="true" />}</ItemMedia>
      <ItemContent className="min-w-0">
        {/* Wraps instead of truncating: names that share a prefix differ at the end. */}
        <ItemTitle className="line-clamp-none w-full min-w-0">
          <span dir="auto" className="min-w-0 text-left [overflow-wrap:anywhere]">
            {name}
          </span>
        </ItemTitle>
        <ItemDescription className="line-clamp-none [overflow-wrap:anywhere]">{what}</ItemDescription>
        <ItemDescription>
          {t("settings.mcp.created", { time: ago(g.createdAt) })} · {used}
        </ItemDescription>
      </ItemContent>
      <ItemActions>
        <AlertDialog>
          <AlertDialogTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t("settings.mcp.revoke", { name })} />}>
            <TrashIcon aria-hidden="true" />
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("settings.mcp.revokeTitle")}</AlertDialogTitle>
              <AlertDialogDescription>
                <span dir="auto" className="[overflow-wrap:anywhere]">
                  {t("settings.mcp.revokeBody", { name })}
                </span>
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("common.keep")}</AlertDialogCancel>
              <AlertDialogAction variant="danger" onClick={onRevoke}>
                {t("settings.mcp.revokeAction")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </ItemActions>
    </Item>
  )
}
