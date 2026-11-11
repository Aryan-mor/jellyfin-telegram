import { useEffect, useState, type ReactNode } from "react"
import { NavLink, Outlet, useLocation } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Bot, Cable, CloudUpload, Film, Filter, Globe, Home, HardDrive, Images, KeyRound, Languages, Loader2, Menu, Moon, Newspaper,
  Radio, ScrollText, Send, Share2, Sun, Tv, UserRound,
} from "lucide-react"
import { toast } from "sonner"
import { api, panelCode, pausePolling, pollingPaused } from "@/lib/api"
import { adoptServerLanguage, t, UiLanguageSelect, useUiLang } from "@/lib/i18n"
import { Button, Sheet } from "@/components/ui"
import { confirmDialog } from "@/components/widgets"
import { cn } from "@/lib/utils"

export type BotCounters = { reads: number; read_bytes: number; writes: number; failures: number; floods: number; running: number; last?: number; wait_until?: number }

export type PanelState = {
  api_id?: number
  default_api?: boolean
  channel_id: number
  channel_title?: string
  language: string
  ui_language?: string
  bots: { id: number; name: string; hint: string; ok: boolean; admin: boolean; connected: boolean; error?: string; counters?: BotCounters }[]
  addresses: { url: string; away: boolean }[]
  seerr: boolean
  pace?: Record<string, unknown> | null
  telegram_search: boolean
  mirrors?: { id: number; channel_id: number; title: string; bot_id: number; kinds: string[]; error: string; sent: number; username?: string }[]
  local: boolean
  sources: boolean
  telegram?: { sessions?: Session[] }
  accounts: { app: string; path: string; user?: string; password?: string; note?: string }[]
  forum?: Record<string, unknown> | null
  paused?: boolean
  // starting is set while the process is still coming up. The reply is empty on purpose.
  starting?: boolean
}

export type Session = {
  user_id: number; username?: string; phone?: string; connected?: boolean; admin?: boolean; premium?: boolean
  max_bytes?: number; error?: string; busy?: boolean; progress?: number; total?: number
}

export function usePanel() {
  return useQuery({
    queryKey: ["panel-state"],
    queryFn: ({ signal }) => api<PanelState>("/panel/state", undefined, AbortSignal.any([signal, AbortSignal.timeout(4000)])),
    retry: (_count, error) => unreachable(error),
    retryDelay: 1000,
    refetchIntervalInBackground: true,
    refetchInterval: (q) => {
      // Keep asking after retries, too. A dead poll must not turn into "no account".
      // A hidden tab still has to notice the server came back.
      if (unreachable(q.state.error) || q.state.data?.starting) return 1000
      if (pollingPaused()) return false
      return 8000
    },
  })
}

export function useJob(enabled = true) {
  return useQuery({
    queryKey: ["panel-job"],
    enabled,
    queryFn: () => api<{ running: boolean; progress: { text: string; state: string }[]; error?: string; restart?: boolean; result?: { link?: string; bots?: string[] }; owned?: { username: string; name?: string }[] }>("/panel/jobs"),
    refetchInterval: (q) => (pollingPaused() ? false : q.state.data?.running ? 1000 : 4000),
  })
}

export function useAutomation() {
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  async function setPaused(paused: boolean) {
    if (paused) {
      const ok = await confirmDialog(
        t("Pause automatic work?"),
        t("Bots and accounts stop searching, copying and uploading on their own. Watching and this panel keep working."),
        t("Pause automatic work"),
        false,
      )
      if (!ok) return
    }
    setBusy(true)
    try {
      await api("/panel/automation", { paused })
      await qc.invalidateQueries({ queryKey: ["panel-state"] })
      toast(paused ? t("Automatic work is paused") : t("Automatic work resumed"))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("Something went wrong"))
    } finally {
      setBusy(false)
    }
  }
  return { busy, setPaused }
}

// unreachable is a server that is down or still booting. A real refusal (wrong code, not this
// computer) has its own message and must not sit on the preparing screen.
function unreachable(error: unknown) {
  if (error instanceof TypeError) return true
  if (typeof DOMException !== "undefined" && error instanceof DOMException) return true
  if (!(error instanceof Error)) return false
  if (error.name === "TimeoutError" || error.name === "AbortError") return true
  const msg = error.message
  return msg === "Failed to fetch" || msg === "Load failed" || msg === "Something went wrong" || msg === "starting" || msg.includes("still starting")
}

function themeNow() {
  const saved = localStorage.getItem("theme")
  if (saved === "light" || saved === "dark") return saved
  return matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
}

export function Shell() {
  useUiLang()
  const state = usePanel()
  const s = state.data
  // A restart answers before Telegram, bots and the library are up, or it does not answer at all.
  // Either way the pages would say nothing is signed in. Hold every page until a finished state arrives.
  const preparing = state.isPending || !!s?.starting || unreachable(state.error)
  const job = useJob(!preparing)
  const auto = useAutomation()
  const update = useQuery({
    queryKey: ["panel-update"],
    enabled: !preparing,
    queryFn: () => api<{ current: string; channel?: string; latest?: string; available: boolean; url?: string }>("/panel/update"),
    staleTime: 0,
    refetchOnMount: "always",
    refetchInterval: 30 * 60 * 1000,
    refetchIntervalInBackground: true,
  })
  const [restart, setRestart] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [installing, setInstalling] = useState(false)
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
  useEffect(() => { adoptServerLanguage(state.data?.ui_language) }, [state.data?.ui_language])
  useEffect(() => {
    const data = update.data
    if (!data?.available || !data.latest) return
    const key = "jftg-update-noted"
    if (sessionStorage.getItem(key) === data.latest) return
    sessionStorage.setItem(key, data.latest)
    const kind = data.channel === "nightly" ? t("nightly") : t("version")
    toast(t("A new {{kind}} is out: {{latest}}", { kind, latest: data.latest }))
  }, [update.data])
  useEffect(() => {
    const q = new URLSearchParams(window.location.search)
    const notice = q.get("notice")
    if (notice !== "updated" && notice !== "installed") return
    toast(notice === "updated" ? t("Updated successfully") : t("Installed successfully"))
    q.delete("notice")
    const next = q.toString()
    window.history.replaceState(null, "", window.location.pathname + (next ? "?" + next : "") + window.location.hash)
  }, [])
  if (preparing) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-3 p-6 text-center">
        <Loader2 className="size-8 animate-spin text-muted-foreground" />
        <p className="text-sm text-muted-foreground">{t("The server is getting ready.")}</p>
      </div>
    )
  }
  if (state.isError && !s) {
    return (
      <div className="flex min-h-screen items-center justify-center p-6">
        <p className="text-sm text-destructive">{(state.error as Error).message}</p>
      </div>
    )
  }
  const signed = (s?.telegram?.sessions?.length ?? 0) > 0
  const botPending = (s?.bots ?? []).some((b) => !b.connected)
  const botBad = (s?.bots ?? []).some((b) => b.connected && (!b.ok || !b.admin || b.error))
  const nav = (
    <nav className="flex h-full flex-col gap-4 text-sm">
      <div className="flex items-center gap-2 px-2 text-sm font-semibold"><span className="flex size-7 items-center justify-center rounded-md bg-primary text-primary-foreground"><Send className="size-3.5" /></span>jellyfin-telegram</div>
      <Nav to="/" label={t("Overview")} icon={<Home className="size-4" />} />
      <Nav to="/accounts" label={t("Sign in details")} icon={<KeyRound className="size-4" />} />
      <Group title={t("Library")}>
        <Nav to="/channel" label={t("Main channel")} icon={<Tv className="size-4" />} />
        <Nav to="/channel/mirrors" label={t("Mirrors")} icon={<Radio className="size-4" />} />
        <Nav to="/channel/shared" label={t("Shared library")} icon={<Share2 className="size-4" />} />
        <Nav to="/channel/home" label={t("Home videos")} icon={<Images className="size-4" />} />
        <Nav to="/channel/subtitles" label={t("Subtitles")} icon={<Languages className="size-4" />} />
      </Group>
      <Group title="Telegram">
        <Nav to="/telegram" label={t("Accounts")} icon={<UserRound className="size-4" />} dot={!signed} />
        <Nav to="/bots" label={t("Bots")} icon={<Bot className="size-4" />} dot={botBad || botPending} />
        <Nav to="/logs" label={t("Logs")} icon={<ScrollText className="size-4" />} />
        <Nav to="/api" label={t("API")} icon={<Cable className="size-4" />} />
      </Group>
      <Group title={t("Sources")}>
        <Nav to="/sources/filter" label={t("Auto-add filter")} icon={<Filter className="size-4" />} />
        {s?.sources && signed && <Nav to="/sources" label={t("Direct files")} icon={<Film className="size-4" />} />}
        {s?.sources && signed && <Nav to="/news" label={t("News channels")} icon={<Newspaper className="size-4" />} />}
        {s?.local && <Nav to="/local" label={t("Local files")} icon={<HardDrive className="size-4" />} />}
        {s?.pace && signed && <Nav to="/upload-pace" label={t("Upload pace")} icon={<CloudUpload className="size-4" />} />}
      </Group>
      <Group title={t("Access")}>
        <Nav to="/away" label={t("Away from home")} icon={<Globe className="size-4" />} />
      </Group>
      <div className="mt-auto space-y-2 px-2">
        <UiLanguageSelect />
        <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">{t("Appearance")}{update.data?.current ? ` · v${update.data.current}` : ""}</span>
        <Button type="button" size="icon" variant="ghost" aria-label={t("Switch light or dark theme")} onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
          {theme === "dark" ? <Sun className="size-4" /> : <Moon className="size-4" />}
        </Button>
        </div>
      </div>
    </nav>
  )
  return (
    <div className="min-h-screen md:grid md:grid-cols-[15rem_1fr]">
      <aside className="hidden border-r bg-card p-3 md:block">{nav}</aside>
      <Sheet open={open} onOpenChange={setOpen}><div onClick={() => setOpen(false)}>{nav}</div></Sheet>
      <div className="min-w-0">
        <div className="flex items-center gap-2 border-b p-2 md:hidden">
          <Button type="button" size="icon" variant="ghost" aria-label={t("Open menu")} onClick={() => setOpen(true)}><Menu className="size-4" /></Button>
          <span className="text-sm font-medium">{t("Panel")}</span>
        </div>
        {s?.paused && (
          <div className="flex flex-wrap items-center justify-between gap-3 bg-rose-500/15 px-4 py-2 text-sm">
            <span>{t("Automatic work is paused. Playback and this panel keep working. Bots and accounts do not start new work until you resume.")}</span>
            <Button size="sm" disabled={auto.busy} onClick={() => void auto.setPaused(false)}>{t("Start again")}</Button>
          </div>
        )}
        {update.data?.available && (
          <div className="flex flex-wrap items-center justify-between gap-3 bg-sky-500/15 px-4 py-2 text-sm">
            <span>
              {t("A new {{kind}} is out: {{latest}} (you have {{current}}). Install it here, or open the release page. This stays on this channel, and your settings and library are kept.", { kind: update.data.channel === "nightly" ? t("nightly") : t("version"), latest: update.data.latest ?? "", current: update.data.current ?? "" })}
            </span>
            <span className="flex items-center gap-3">
              <Button size="sm" disabled={installing} onClick={() => void installUpdate(setInstalling)}>{installing ? t("Installing") : t("Install update")}</Button>
              {update.data.url && <a className="font-medium underline" href={update.data.url} target="_blank" rel="noreferrer">{t("Release page")}</a>}
            </span>
          </div>
        )}
        {(restart || botPending) && (
          <div className="flex flex-wrap items-center justify-between gap-3 bg-amber-500/15 px-4 py-2 text-sm">
            <span>{botPending
              ? t("A saved bot is not connected yet. Restart so it starts working.")
              : t("Restart needed. New settings apply the next time the program starts.")}</span>
            <Button size="sm" disabled={restarting} onClick={() => void restartProgram(setRestarting)}>{restarting ? t("Restarting") : t("Restart")}</Button>
          </div>
        )}
        <main className="mx-auto max-w-4xl space-y-4 p-4">
          {state.isError && !installing && !restarting && <p className="text-sm text-destructive">{(state.error as Error).message}</p>}
          <Outlet />
        </main>
      </div>
    </div>
  )
}

// giveUpIfSteady stops waiting when the program never went away. A real failure
// (download rejected, already installed) leaves it up. A restart takes it down first.
async function waitUntilBack(giveUpIfSteady = false) {
  let sawDown = false
  for (let i = 0; i < 90; i++) {
    await new Promise((r) => setTimeout(r, 1000))
    let up = false
    try {
      const res = await fetch("/panel/state?code=" + encodeURIComponent(panelCode()), { cache: "no-store" })
      up = res.ok
    } catch {
      up = false
    }
    if (!up) sawDown = true
    else if (sawDown) return true
    else if (giveUpIfSteady && i >= 4) return false
  }
  return false
}

async function installUpdate(setBusy: (v: boolean) => void) {
  const ok = await confirmDialog(t("Install this update?"), t("Playback stops while the new version is installed. This usually takes under a minute."), t("Install update"), false)
  if (!ok) return
  setBusy(true)
  pausePolling(true)
  try {
    await api("/panel/update", {})
    toast(t("Installing"))
    if (await waitUntilBack()) {
      location.reload()
      return
    }
    toast.error(t("The program did not come back. Start it again from this computer."))
    setBusy(false)
  } catch (e) {
    // The reply is often lost because the program restarts while the browser is still reading it.
    if (await waitUntilBack(true)) {
      location.reload()
      return
    }
    toast.error(e instanceof Error ? e.message : t("The update failed"))
    setBusy(false)
  } finally {
    pausePolling(false)
  }
}

async function restartProgram(setBusy: (v: boolean) => void) {
  const ok = await confirmDialog(t("Restart now?"), t("Playback stops until the program is back. This usually takes under a minute."), t("Restart"), false)
  if (!ok) return
  setBusy(true)
  pausePolling(true)
  try {
    await api("/panel/restart", {})
    toast(t("Restarting"))
    if (await waitUntilBack()) {
      location.reload()
      return
    }
    toast.error(t("The program did not come back. Start it again from this computer."))
    setBusy(false)
  } catch (e) {
    if (await waitUntilBack(true)) {
      location.reload()
      return
    }
    toast.error(e instanceof Error ? e.message : t("Restart failed"))
    setBusy(false)
  } finally {
    pausePolling(false)
  }
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return <div className="space-y-1"><div className="px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{title}</div>{children}</div>
}

function Nav({ to, label, icon, dot }: { to: string; label: string; icon?: ReactNode; dot?: boolean }) {
  const { search } = useLocation()
  return (
    <NavLink to={{ pathname: to, search }} end={to === "/" || to === "/channel"} className={({ isActive }) => cn("flex h-8 items-center gap-2 rounded-md px-2", isActive ? "bg-accent font-medium" : "hover:bg-accent/60")}>
      {icon ?? <span className="size-4" />}
      <span className="flex-1">{label}</span>
      {dot && <span className="size-2 rounded-full bg-amber-500" />}
    </NavLink>
  )
}
