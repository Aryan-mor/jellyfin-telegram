import type { ReactNode } from "react"
import { useSearchParams } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { api, pollingPaused } from "@/lib/api"
import { t } from "@/lib/i18n"
import { ago, cn } from "@/lib/utils"
import { Button, Card, Empty, Table, TBody, TD, TH, THead, TR } from "@/components/ui"
import { usePanel, type BotCounters } from "@/panel/shell"

type LogRow = { id: number; at: number; action: string; detail: string; ok: boolean; error?: string }
type LogPage = { items: LogRow[]; total: number; page: number; pages: number }

export function Logs() {
  const { data: s } = usePanel()
  const [params, setParams] = useSearchParams()
  const kind = params.get("kind") === "bot" ? "bot" : params.get("kind") === "account" ? "account" : ""
  const id = Number(params.get("id") || 0)
  const page = Math.max(1, Number(params.get("page") || 1))
  const selected = (kind === "account" || kind === "bot") && id > 0
  const logs = useQuery({
    queryKey: ["panel-logs", kind, id, page],
    enabled: selected,
    queryFn: () => api<LogPage>(`/panel/activity?kind=${kind}&id=${id}&page=${page}`),
    refetchInterval: () => (pollingPaused() ? false : 8000),
  })

  function select(nextKind: "account" | "bot", nextID: number) {
    const next = new URLSearchParams(params)
    next.set("kind", nextKind)
    next.set("id", String(nextID))
    next.delete("page")
    setParams(next)
  }
  function go(nextPage: number) {
    const next = new URLSearchParams(params)
    if (nextPage <= 1) next.delete("page")
    else next.set("page", String(nextPage))
    setParams(next)
  }

  const accounts = s?.telegram?.sessions ?? []
  const bots = s?.bots ?? []
  const rows = logs.data?.items ?? []
  const pages = logs.data?.pages ?? 1
  const total = logs.data?.total ?? 0
  const counters = kind === "bot" ? bots.find((b) => b.id === id)?.counters : undefined

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{t("Logs")}</h1>
        <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{t("Pick an account or a bot to see what it did.")}</p>
      </div>
      <div className="grid items-start gap-4 md:grid-cols-[16rem_1fr]">
        <Card className="overflow-hidden">
          <Group title={t("Accounts")}>
            {accounts.map((ses) => {
              const name = ses.username ? "@" + ses.username : (ses.phone || t("user {{id}}", { id: ses.user_id }))
              return <Pick key={ses.user_id} label={name} on={kind === "account" && id === ses.user_id} onClick={() => select("account", ses.user_id)} />
            })}
            {!accounts.length && <p className="px-3 py-2 text-sm text-muted-foreground">{t("No Telegram account is signed in.")}</p>}
          </Group>
          <Group title={t("Bots")}>
            {bots.map((bot) => (
              <Pick key={bot.id} label={bot.name ? "@" + bot.name.replace(/^@/, "") : t("bot {{id}}", { id: bot.id })} on={kind === "bot" && id === bot.id} onClick={() => select("bot", bot.id)} />
            ))}
            {!bots.length && <p className="px-3 py-2 text-sm text-muted-foreground">{t("No bots yet.")}</p>}
          </Group>
        </Card>
        <Card>
          {!selected && <Empty>{t("Pick an account or a bot.")}</Empty>}
          {selected && logs.isError && <p className="p-4 text-sm text-destructive">{(logs.error as Error).message}</p>}
          {selected && !logs.isError && (
            <>
              <div className="flex items-center justify-between gap-2 border-b px-4 py-3">
                <h2 className="font-medium">{t("{{n}} events", { n: total })}</h2>
                <div className="flex items-center gap-2">
                  <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => go(page - 1)}>{t("Previous")}</Button>
                  <span className="text-xs text-muted-foreground">{t("Page {{page}} of {{pages}}", { page, pages })}</span>
                  <Button size="sm" variant="outline" disabled={page >= pages} onClick={() => go(page + 1)}>{t("Next")}</Button>
                </div>
              </div>
              {kind === "bot" && <Counts c={counters} />}
              <Table>
                <THead>
                  <TR>
                    <TH>{t("When")}</TH>
                    <TH>{t("Action")}</TH>
                    <TH>{t("Detail")}</TH>
                    <TH>{t("Result")}</TH>
                  </TR>
                </THead>
                <TBody>
                  {rows.map((row) => (
                    <TR key={row.id}>
                      <TD className="whitespace-nowrap text-muted-foreground"><span title={new Date(row.at * 1000).toLocaleString()}>{ago(row.at)}</span></TD>
                      <TD>{t(row.action)}</TD>
                      <TD className="max-w-xs truncate"><span title={row.detail}>{row.detail}</span></TD>
                      <TD className={row.ok ? "text-muted-foreground" : "text-destructive"}>{row.ok ? t("ok") : (row.error || t("failed"))}</TD>
                    </TR>
                  ))}
                </TBody>
              </Table>
              {!logs.isLoading && !rows.length && <Empty>{t("Nothing here yet.")}</Empty>}
            </>
          )}
        </Card>
      </div>
    </div>
  )
}

function mib(n: number) {
  return n >= 1 << 30 ? (n / (1 << 30)).toFixed(1) + " GiB" : (n / (1 << 20)).toFixed(0) + " MiB"
}

// What this bot did since the service started. Reading file blocks is not in the event list below,
// so it shows here.
function Counts({ c }: { c?: BotCounters }) {
  if (!c) return <p className="border-b px-4 py-3 text-sm text-muted-foreground">{t("No counters yet. They start when the bot connects.")}</p>
  const wait = c.wait_until && c.wait_until * 1000 > Date.now() ? Math.ceil((c.wait_until * 1000 - Date.now()) / 1000) : 0
  const items: [string, string][] = [
    [t("Blocks read"), String(c.reads)],
    [t("Data read"), mib(c.read_bytes)],
    [t("Channel calls"), String(c.writes)],
    [t("Refused"), String(c.failures)],
    [t("Flood waits"), String(c.floods)],
    [t("Last work"), c.last ? ago(c.last) : "–"],
  ]
  return (
    <div className="border-b px-4 py-3">
      <div className="grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-3">
        {items.map(([k, v]) => <div key={k}><div className="text-xs text-muted-foreground">{k}</div><div className="text-sm font-medium tabular-nums">{v}</div></div>)}
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        {wait ? t("Resting for {{n}} s after a flood wait.", { n: wait }) : c.running ? t("Working now.") : t("Free.")} {t("Counted since the service started.")}
      </p>
    </div>
  )
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="border-b last:border-b-0">
      <div className="px-3 pt-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{title}</div>
      <div className="p-2">{children}</div>
    </div>
  )
}

function Pick({ label, on, onClick }: { label: string; on: boolean; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className={cn("block w-full truncate rounded-md px-2 py-1.5 text-left text-sm", on ? "bg-accent font-medium" : "hover:bg-accent/60")}>
      {label}
    </button>
  )
}
