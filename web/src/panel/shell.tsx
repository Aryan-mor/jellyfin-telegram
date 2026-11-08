import { useEffect, useState, type ReactNode } from "react"
import { NavLink, Outlet } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Home, KeyRound, Menu, Moon, Sun } from "lucide-react"
import { toast } from "sonner"
import { api, panelCode, pollingPaused } from "@/lib/api"
import { Button, Sheet } from "@/components/ui"
import { confirmDialog } from "@/components/widgets"
import { cn } from "@/lib/utils"

export type PanelState = {
  api_id?: number
  default_api?: boolean
  channel_id: number
  language: string
  bots: { id: number; name: string; hint: string; ok: boolean; admin: boolean; connected: boolean; error?: string }[]
  addresses: { url: string; away: boolean }[]
  seerr: boolean
  fladder: boolean
  downloads?: Record<string, unknown> | null
  telegram_search: boolean
  mirrors?: { id: number; channel_id: number; title: string; bot_id: number; kinds: string[]; error: string; sent: number; username?: string }[]
  local: boolean
  sources: boolean
  telegram?: { sessions?: Session[] }
  accounts: { app: string; path: string; user?: string; password?: string; note?: string }[]
  forum?: Record<string, unknown> | null
}

export type Session = {
  user_id: number; username?: string; phone?: string; connected?: boolean; admin?: boolean; premium?: boolean
  max_bytes?: number; error?: string; busy?: boolean; progress?: number; total?: number
}

export function usePanel() {
  return useQuery({
    queryKey: ["panel-state"],
    queryFn: () => api<PanelState>("/panel/state"),
    refetchInterval: () => (pollingPaused() ? false : 8000),
  })
}

export function useJob() {
  return useQuery({
    queryKey: ["panel-job"],
    queryFn: () => api<{ running: boolean; progress: { text: string; state: string }[]; error?: string; restart?: boolean; result?: { link?: string; bots?: string[] }; owned?: { username: string; name?: string }[] }>("/panel/jobs"),
    refetchInterval: (q) => (pollingPaused() ? false : q.state.data?.running ? 1000 : 4000),
  })
}

function themeNow() {
  const saved = localStorage.getItem("theme")
  if (saved === "light" || saved === "dark") return saved
  return matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
}

export function Shell() {
  const state = usePanel()
  const job = useJob()
  const [restart, setRestart] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [open, setOpen] = useState(false)
  const [theme, setTheme] = useState(themeNow)
  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    localStorage.setItem("theme", theme)
  }, [theme])
  useEffect(() => {
    const on = () => setRestart(true)
    window.addEventListener("panel-restart", on)
    return () => window.removeEventListener("panel-restart", on)
  }, [])
  useEffect(() => { if (job.data?.restart) setRestart(true) }, [job.data?.restart])
  const s = state.data
  const signed = (s?.telegram?.sessions?.length ?? 0) > 0
  const botBad = (s?.bots ?? []).some((b) => b.connected && (!b.ok || !b.admin || b.error))
  const nav = (
    <nav className="flex h-full flex-col gap-4 text-sm">
      <div className="px-2 text-sm font-semibold">jellyfin-telegram</div>
      <Nav to="/" label="Overview" icon={<Home className="size-4" />} />
      <Nav to="/accounts" label="Sign in details" icon={<KeyRound className="size-4" />} />
      <Group title="Library">
        <Nav to="/channel" label="Main channel" />
        <Nav to="/channel/mirrors" label="Mirrors" />
        <Nav to="/channel/shared" label="Shared library" />
        <Nav to="/channel/home" label="Home videos" />
        <Nav to="/channel/subtitles" label="Subtitles" />
      </Group>
      <Group title="Telegram">
        <Nav to="/telegram" label="Accounts" dot={!signed} />
        <Nav to="/bots" label="Bots" dot={botBad} />
        <Nav to="/api" label="API" />
      </Group>
      <Group title="Sources">
        {s?.sources && signed && <Nav to="/sources" label="Direct files" />}
        {s?.sources && signed && <Nav to="/news" label="News channels" />}
        {s?.local && <Nav to="/local" label="Local files" />}
        {s?.downloads && signed && <Nav to="/downloads" label="Torrents" />}
      </Group>
      <Group title="Access">
        <Nav to="/away" label="Away from home" />
      </Group>
      <div className="mt-auto flex items-center justify-between px-2">
        <span className="text-xs text-muted-foreground">Appearance</span>
        <Button type="button" size="icon" variant="ghost" aria-label="Switch light or dark theme" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
          {theme === "dark" ? <Sun className="size-4" /> : <Moon className="size-4" />}
        </Button>
      </div>
    </nav>
  )
  return (
    <div className="min-h-screen md:grid md:grid-cols-[15rem_1fr]">
      <aside className="hidden border-r bg-card p-3 md:block">{nav}</aside>
      <Sheet open={open} onOpenChange={setOpen}><div onClick={() => setOpen(false)}>{nav}</div></Sheet>
      <div className="min-w-0">
        <div className="flex items-center gap-2 border-b p-2 md:hidden">
          <Button type="button" size="icon" variant="ghost" aria-label="Open menu" onClick={() => setOpen(true)}><Menu className="size-4" /></Button>
          <span className="text-sm font-medium">Panel</span>
        </div>
        {restart && (
          <div className="flex items-center justify-between gap-3 bg-amber-500/15 px-4 py-2 text-sm">
            <span>Restart needed. New settings apply the next time the program starts.</span>
            <Button size="sm" disabled={restarting} onClick={() => void restartProgram(setRestarting)}>{restarting ? "Restarting" : "Restart"}</Button>
          </div>
        )}
        <main className="mx-auto max-w-4xl space-y-4 p-4">
          {state.isError && <p className="text-sm text-destructive">{(state.error as Error).message}</p>}
          <Outlet />
        </main>
      </div>
    </div>
  )
}

async function restartProgram(setBusy: (v: boolean) => void) {
  const ok = await confirmDialog("Restart now?", "Playback stops until the program is back. This usually takes under a minute.", "Restart")
  if (!ok) return
  setBusy(true)
  try {
    await api("/panel/restart", {})
    toast("Restarting")
    let sawDown = false
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 1000))
      let up = false
      try {
        const res = await fetch("/panel/state?code=" + encodeURIComponent(panelCode()), { cache: "no-store" })
        up = res.ok
      } catch {
        up = false
      }
      if (!up) sawDown = true
      else if (sawDown) {
        location.reload()
        return
      }
    }
    toast.error("The program did not come back. Start it again from this computer.")
    setBusy(false)
  } catch (e) {
    toast.error(e instanceof Error ? e.message : "Restart failed")
    setBusy(false)
  }
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return <div className="space-y-1"><div className="px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{title}</div>{children}</div>
}

function Nav({ to, label, icon, dot }: { to: string; label: string; icon?: ReactNode; dot?: boolean }) {
  return (
    <NavLink to={to} end={to === "/" || to === "/channel"} className={({ isActive }) => cn("flex h-8 items-center gap-2 rounded-md px-2", isActive ? "bg-accent font-medium" : "hover:bg-accent/60")}>
      {icon ?? <span className="size-4" />}
      <span className="flex-1">{label}</span>
      {dot && <span className="size-2 rounded-full bg-amber-500" />}
    </NavLink>
  )
}
