import { useEffect, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { api, pollingPaused } from "@/lib/api"
import { t } from "@/lib/i18n"
import { Card } from "@/components/ui"
import { usePanel, type PanelState } from "@/panel/shell"

export type LiveRow = {
  id: number; at: number; action: string; detail: string; ok: boolean; error?: string
  kind: string; actor_id: number; name: string
}

// useFeed is the newest events of every account and bot. The chart, the users table and the live log share it.
export function useFeed() {
  return useQuery({
    queryKey: ["panel-logs", "all"],
    queryFn: () => api<{ items: LiveRow[] }>("/panel/activity?kind=all&page=1"),
    refetchInterval: () => (pollingPaused() ? false : 3000),
  })
}

// ---------- samples ----------

type Pt = { t: number; v: number; off: boolean }

const WINDOW = 5 * 60 * 1000
// Kept outside the component so the lines stay when the person opens another page and comes back.
const store = new Map<string, Pt[]>()
const lastTotal = new Map<string, number>()
let lastEventID = -1

function push(key: string, v: number, off: boolean) {
  const now = Date.now()
  const list = (store.get(key) ?? []).filter((p) => now - p.t <= WINDOW + 30_000)
  list.push({ t: now, v, off })
  store.set(key, list)
}

type Line = { key: string; label: string; color: string; off: boolean; latest: number; unit: string }

const HUES = [210, 20, 150, 285, 45, 340, 180, 100, 245, 0]
const colorAt = (i: number) => `hsl(${HUES[i % HUES.length]} 72% 56%)`

function nameOfBot(b: PanelState["bots"][number]) {
  return b.name ? "@" + b.name.replace(/^@/, "") : t("bot {{id}}", { id: b.id })
}

export function LiveChart() {
  const { data: s, dataUpdatedAt: stateAt } = usePanel()
  const feed = useFeed()
  const [now, setNow] = useState(() => Date.now())
  const [hide, setHide] = useState<Set<string>>(new Set())

  // The panel state is asked for more often here, so the lines follow the bots closely.
  useQuery({
    queryKey: ["panel-state"],
    queryFn: () => api<PanelState>("/panel/state"),
    refetchInterval: () => (pollingPaused() ? false : 4000),
  })

  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(id)
  }, [])

  // Bots: how much they worked since the last look. The counters only go up, so the change is the work.
  useEffect(() => {
    if (!s) return
    for (const b of s.bots) {
      const key = "bot:" + b.id
      const c = b.counters
      const total = c ? c.reads + c.writes + c.failures : 0
      const before = lastTotal.get(key)
      push(key, before === undefined ? 0 : Math.max(0, total - before), !b.connected)
      lastTotal.set(key, total)
    }
  }, [stateAt, s])

  // Accounts: the events they wrote since the last look.
  const feedAt = feed.dataUpdatedAt
  useEffect(() => {
    const rows = feed.data?.items
    if (!rows || !s) return
    const top = rows.reduce((m, r) => Math.max(m, r.id), 0)
    const fresh = lastEventID < 0 ? [] : rows.filter((r) => r.id > lastEventID)
    lastEventID = Math.max(lastEventID, top)
    for (const ses of s.telegram?.sessions ?? []) {
      push("account:" + ses.user_id, fresh.filter((r) => r.kind === "account" && r.actor_id === ses.user_id).length, !ses.connected)
    }
  }, [feedAt]) // eslint-disable-line react-hooks/exhaustive-deps

  if (!s) return null
  const lines: Line[] = []
  s.bots.forEach((b) => lines.push({ key: "bot:" + b.id, label: nameOfBot(b), color: "", off: !b.connected, latest: 0, unit: t("blocks") }))
  ;(s.telegram?.sessions ?? []).forEach((a) => lines.push({
    key: "account:" + a.user_id,
    label: a.username ? "@" + a.username : (a.phone || t("user {{id}}", { id: a.user_id })),
    color: "", off: !a.connected, latest: 0, unit: t("events"),
  }))
  lines.forEach((l, i) => {
    l.color = colorAt(i)
    l.latest = store.get(l.key)?.at(-1)?.v ?? 0
  })

  const W = 1000, H = 220, padTop = 14, padBottom = 10
  const from = now - WINDOW
  const x = (tm: number) => ((tm - from) / WINDOW) * W
  const drawn = lines.filter((l) => !hide.has(l.key))

  return (
    <Card>
      <div className="space-y-3 p-4">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2 className="font-medium">{t("Activity")}</h2>
          <p className="text-xs text-muted-foreground">{t("Last 5 minutes. A line drops to the bottom when it is off or idle, and rises as it works more.")}</p>
        </div>
        <div className="relative">
          <div className="pointer-events-none absolute inset-y-0 left-0 flex flex-col justify-between py-1 text-[10px] text-muted-foreground">
            <span>{t("Busy")}</span><span>{t("Idle")}</span>
          </div>
          <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="ml-8 h-56 w-[calc(100%-2rem)] overflow-visible" role="img" aria-label={t("Activity of every bot and account")}>
            <defs>
              {drawn.map((l) => (
                <linearGradient key={l.key} id={"g-" + l.key.replace(":", "-")} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={l.color} stopOpacity="0.28" />
                  <stop offset="100%" stopColor={l.color} stopOpacity="0" />
                </linearGradient>
              ))}
            </defs>
            {[0, 1, 2, 3].map((i) => {
              const y = padTop + (i / 3) * (H - padTop - padBottom)
              return <line key={i} x1="0" x2={W} y1={y} y2={y} stroke="currentColor" strokeOpacity={i === 3 ? 0.25 : 0.1} strokeDasharray={i === 3 ? undefined : "4 6"} vectorEffect="non-scaling-stroke" />
            })}
            {drawn.map((l) => {
              const pts = (store.get(l.key) ?? []).filter((p) => p.t >= from - 20_000)
              if (pts.length < 2) return null
              const peak = Math.max(1, ...pts.map((p) => p.v))
              const xy = pts.map((p) => [x(p.t), H - padBottom - (p.v / peak) * (H - padTop - padBottom)] as const)
              const d = smooth(xy)
              const area = `${d} L${xy[xy.length - 1][0]} ${H - padBottom} L${xy[0][0]} ${H - padBottom} Z`
              return (
                <g key={l.key} opacity={l.off ? 0.55 : 1}>
                  <path d={area} fill={`url(#g-${l.key.replace(":", "-")})`} />
                  <path d={d} fill="none" stroke={l.color} strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round" strokeDasharray={l.off ? "5 5" : undefined} vectorEffect="non-scaling-stroke" />
                </g>
              )
            })}
          </svg>
          <div className="ml-8 mt-1 flex justify-between text-[10px] text-muted-foreground">
            <span>{t("5 min ago")}</span><span>{t("2.5 min ago")}</span><span>{t("Now")}</span>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {lines.map((l) => {
            const off = hide.has(l.key)
            return (
              <button
                key={l.key}
                type="button"
                onClick={() => setHide((h) => { const n = new Set(h); if (n.has(l.key)) n.delete(l.key); else n.add(l.key); return n })}
                className="flex items-center gap-2 rounded-full border px-2.5 py-1 text-xs transition-opacity hover:bg-accent/50"
                style={{ opacity: off ? 0.4 : 1 }}
                title={t("Show or hide this line")}
              >
                <span className="size-2.5 rounded-full" style={{ background: l.color }} />
                <span className="font-medium">{l.label}</span>
                <span className="text-muted-foreground">{l.off ? t("Off") : `${l.latest} ${l.unit}`}</span>
              </button>
            )
          })}
          {!lines.length && <p className="text-sm text-muted-foreground">{t("No bots or accounts yet.")}</p>}
        </div>
      </div>
    </Card>
  )
}

// smooth joins the points with a curve that stays between them, so a quiet stretch does not bulge.
function smooth(p: readonly (readonly [number, number])[]) {
  let d = `M${p[0][0]} ${p[0][1]}`
  for (let i = 1; i < p.length; i++) {
    const mx = (p[i - 1][0] + p[i][0]) / 2
    d += ` C${mx} ${p[i - 1][1]} ${mx} ${p[i][1]} ${p[i][0]} ${p[i][1]}`
  }
  return d
}
