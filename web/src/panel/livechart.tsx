import { useEffect, useRef, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { api, pollingPaused } from "@/lib/api"
import { t } from "@/lib/i18n"
import { Card } from "@/components/ui"
import { usePanel } from "@/panel/shell"

export type LiveRow = {
  id: number; at: number; action: string; detail: string; ok: boolean; error?: string
  kind: string; actor_id: number; name: string
}

// useFeed is the newest events of every account and bot. The users table and the live log share it.
export function useFeed() {
  return useQuery({
    queryKey: ["panel-logs", "all"],
    queryFn: () => api<{ items: LiveRow[] }>("/panel/activity?kind=all&page=1"),
    refetchInterval: () => (pollingPaused() ? false : 3000),
  })
}

type Point = { t: number; work: number; bytes?: number }
type Series = { key: string; points: Point[] }
type SeriesReply = { step: number; window: number; now: number; series: Series[] }

const SHOW = 5 * 60 // seconds on screen
const HUES = [210, 20, 150, 285, 45, 340, 180, 100, 245, 0]
const colorAt = (i: number) => `hsl(${HUES[i % HUES.length]} 72% 56%)`

type Line = { key: string; label: string; color: string; off: boolean; bot: boolean; points: Point[]; peak: number }

function mib(n: number) {
  return n >= 1 << 30 ? (n / (1 << 30)).toFixed(1) + " GiB" : n >= 1 << 20 ? (n / (1 << 20)).toFixed(1) + " MiB" : (n / 1024).toFixed(0) + " KiB"
}

function clock(unix: number) {
  return new Date(unix * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })
}

export function LiveChart() {
  const { data: s } = usePanel()
  const reply = useQuery({
    queryKey: ["panel-series"],
    queryFn: () => api<SeriesReply>("/panel/series"),
    refetchInterval: () => (pollingPaused() ? false : 3000),
  })
  // The server clock and ours may differ, so the right edge is the server's "now" plus the time since it was read.
  const anchor = useRef({ server: 0, local: 0 })
  if (reply.data && anchor.current.server !== reply.data.now) anchor.current = { server: reply.data.now, local: reply.dataUpdatedAt }
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const id = window.setInterval(() => setTick((n) => n + 1), 1000)
    return () => window.clearInterval(id)
  }, [])
  const [hide, setHide] = useState<Set<string>>(new Set())
  const [hover, setHover] = useState<{ x: number; at: number } | null>(null)
  const box = useRef<HTMLDivElement>(null)

  if (!s) return null
  void tick
  const to = anchor.current.server ? anchor.current.server + (Date.now() - anchor.current.local) / 1000 : Math.floor(Date.now() / 1000)
  const from = to - SHOW
  const byKey = new Map((reply.data?.series ?? []).map((x) => [x.key, x.points]))

  const lines: Line[] = []
  s.bots.forEach((b) => lines.push({ key: "bot:" + b.id, label: b.name ? "@" + b.name.replace(/^@/, "") : t("bot {{id}}", { id: b.id }), color: "", off: !b.connected, bot: true, points: [], peak: 1 }))
  ;(s.telegram?.sessions ?? []).forEach((a) => lines.push({
    key: "account:" + a.user_id, label: a.username ? "@" + a.username : (a.phone || t("user {{id}}", { id: a.user_id })),
    color: "", off: !a.connected, bot: false, points: [], peak: 1,
  }))
  lines.forEach((l, i) => {
    l.color = colorAt(i)
    l.points = (byKey.get(l.key) ?? []).filter((p) => p.t >= from - 10)
    l.peak = Math.max(1, ...l.points.map((p) => p.work))
  })

  const W = 1000, H = 220, padTop = 14, padBottom = 10
  const x = (tm: number) => ((tm - from) / SHOW) * W
  const y = (l: Line, v: number) => H - padBottom - (v / l.peak) * (H - padTop - padBottom)
  const drawn = lines.filter((l) => !hide.has(l.key))

  function move(e: React.PointerEvent) {
    const r = box.current?.getBoundingClientRect()
    if (!r) return
    const frac = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width))
    setHover({ x: frac, at: from + frac * SHOW })
  }

  // The reading under the cursor: the point nearest in time for each line.
  const rows = hover ? drawn.map((l) => {
    let best: Point | undefined
    for (const p of l.points) if (!best || Math.abs(p.t - hover.at) < Math.abs(best.t - hover.at)) best = p
    const near = best && Math.abs(best.t - hover.at) <= (reply.data?.step ?? 5) * 1.5 ? best : undefined
    return { l, p: near }
  }) : []

  return (
    <Card>
      <div className="space-y-3 p-4">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2 className="font-medium">{t("Activity")}</h2>
          <p className="text-xs text-muted-foreground">{t("Last 5 minutes. A line drops to the bottom when it is off or idle, and rises as it works more.")}</p>
        </div>
        <div className="relative">
          <div className="pointer-events-none absolute inset-y-0 left-0 flex w-8 flex-col justify-between py-1 text-[10px] text-muted-foreground">
            <span>{t("Busy")}</span><span>{t("Idle")}</span>
          </div>
          <div ref={box} className="relative ml-8 touch-none" onPointerMove={move} onPointerLeave={() => setHover(null)}>
            <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="block h-56 w-full overflow-visible" role="img" aria-label={t("Activity of every bot and account")}>
              <defs>
                {drawn.map((l) => (
                  <linearGradient key={l.key} id={"g-" + l.key.replace(":", "-")} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={l.color} stopOpacity="0.28" />
                    <stop offset="100%" stopColor={l.color} stopOpacity="0" />
                  </linearGradient>
                ))}
              </defs>
              {[0, 1, 2, 3].map((i) => {
                const gy = padTop + (i / 3) * (H - padTop - padBottom)
                return <line key={i} x1="0" x2={W} y1={gy} y2={gy} stroke="currentColor" strokeOpacity={i === 3 ? 0.25 : 0.1} strokeDasharray={i === 3 ? undefined : "4 6"} vectorEffect="non-scaling-stroke" />
              })}
              {drawn.map((l) => {
                // A line with no samples yet is a flat zero, so a new or quiet one still shows.
                const pts: [number, number][] = l.points.length
                  ? l.points.map((p) => [x(p.t), y(l, p.work)])
                  : [[0, y(l, 0)], [W, y(l, 0)]]
                if (pts.length === 1) pts.unshift([0, y(l, 0)])
                const d = smooth(pts)
                const area = `${d} L${pts[pts.length - 1][0]} ${H - padBottom} L${pts[0][0]} ${H - padBottom} Z`
                return (
                  <g key={l.key} opacity={l.off ? 0.55 : 1}>
                    <path d={area} fill={`url(#g-${l.key.replace(":", "-")})`} />
                    <path d={d} fill="none" stroke={l.color} strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round" strokeDasharray={l.off ? "5 5" : undefined} vectorEffect="non-scaling-stroke" />
                  </g>
                )
              })}
            </svg>
            {hover && (
              <>
                <div className="pointer-events-none absolute inset-y-0 w-px bg-foreground/30" style={{ left: `${hover.x * 100}%` }} />
                <div
                  className="pointer-events-none absolute top-1 z-10 min-w-40 rounded-lg border bg-popover px-3 py-2 text-xs shadow-lg"
                  style={hover.x > 0.6 ? { right: `${(1 - hover.x) * 100 + 1.5}%` } : { left: `${hover.x * 100 + 1.5}%` }}
                >
                  <div className="mb-1.5 font-medium">{clock(hover.at)}</div>
                  <div className="space-y-1">
                    {rows.map(({ l, p }) => (
                      <div key={l.key} className="flex items-center gap-2">
                        <span className="size-2.5 shrink-0 rounded-full" style={{ background: l.color }} />
                        <span className="flex-1 truncate">{l.label}</span>
                        <span className="tabular-nums text-muted-foreground">
                          {l.off ? t("Off") : !p ? "–" : l.bot ? `${p.work} ${t("blocks")} · ${mib(p.bytes ?? 0)}` : `${p.work} ${t("events")}`}
                        </span>
                      </div>
                    ))}
                    {!rows.length && <div className="text-muted-foreground">{t("No line is shown.")}</div>}
                  </div>
                </div>
              </>
            )}
          </div>
          <div className="ml-8 mt-1 flex justify-between text-[10px] text-muted-foreground">
            <span>{clock(from)}</span><span>{clock(from + SHOW / 2)}</span><span>{t("Now")}</span>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {lines.map((l) => {
            const gone = hide.has(l.key)
            return (
              <button
                key={l.key}
                type="button"
                onClick={() => setHide((h) => { const n = new Set(h); if (n.has(l.key)) n.delete(l.key); else n.add(l.key); return n })}
                className="flex items-center gap-2 rounded-full border px-2.5 py-1 text-xs transition-opacity hover:bg-accent/50"
                style={{ opacity: gone ? 0.4 : 1 }}
                title={t("Show or hide this line")}
              >
                <span className="size-2.5 rounded-full" style={{ background: l.color }} />
                <span className="font-medium">{l.label}</span>
                <span className="text-muted-foreground">{l.off ? t("Off") : l.points.length ? `${l.points[l.points.length - 1].work} ${l.bot ? t("blocks") : t("events")}` : "0"}</span>
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
