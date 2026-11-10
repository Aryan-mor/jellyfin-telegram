import { StrictMode, useEffect, useRef, useState } from "react"
import { createRoot } from "react-dom/client"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster, toast } from "sonner"
import "@/index.css"
import { api, panelCode } from "@/lib/api"
import { bytes } from "@/lib/utils"
import { I18nProvider, setUiLang, t, uiLang, uiLanguages, useUiLang } from "@/lib/i18n"
import { Button, Card, CardBody, Checkbox, FieldLabel, Input, SwitchBox, Textarea } from "@/components/ui"
import { AutoFilterForm } from "@/components/autofilter"
import { ConfirmHost, confirmDialog, LoginForm, MultiSelect, ProgressSteps } from "@/components/widgets"

const client = new QueryClient()

function randomMirror() {
  const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
  const bytes = new Uint8Array(6)
  crypto.getRandomValues(bytes)
  let suffix = ""
  for (const b of bytes) suffix += letters[b % letters.length]
  return { title: "Films " + suffix, username: "films_" + suffix }
}

type Found = { channel_id: number; title: string; bots: string[]; skipped?: string[] }
type Job = { running: boolean; progress: { text: string; state: string }[]; error?: string; owned?: { username: string; name?: string }[]; kept?: string[]; found?: Found }
type Status = { stage: string; percent: number; ready: boolean; error?: string; urls?: string[]; user?: string; password?: string; seerr?: boolean; fladder?: boolean }
type Step = "language" | "login" | "channel" | "bots" | "prefs" | "filter" | "done"

function languagePicked() {
  return sessionStorage.getItem("jftg-ui-picked") === "1"
}

function Setup() {
  useUiLang()
  const [mode, setMode] = useState<"choose" | "auto" | "manual">("choose")
  const [step, setStep] = useState<Step>(languagePicked() ? "login" : "language")
  const resume = useRef<Step>("login")
  const [available, setAvailable] = useState(true)
  const [found, setFound] = useState<Found | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  const [owned, setOwned] = useState<{ username: string; name?: string }[]>([])
  const [picked, setPicked] = useState<string[]>([])
  const [langs, setLangs] = useState<string[]>([])
  const [title, setTitle] = useState(() => (languagePicked() ? t("Jellyfin library") : ""))
  const [bots, setBots] = useState(5)
  const [channel, setChannel] = useState("")
  const [manual, setManual] = useState({ api_id: "", api_hash: "", channel: "", bots: "" })
  const [prefs, setPrefs] = useState({ fladder: false, seerr: true, downloads: false, group: true, telegram_search: true, jellyfin_url: "", jellyfin_key: "" })
  const [mirrorOn, setMirrorOn] = useState(true)
  const [mirrorMode, setMirrorMode] = useState<"auto" | "have">("auto")
  const [mirrorLink, setMirrorLink] = useState("")
  const [mirror, setMirror] = useState(randomMirror)
  const [ownJellyfin, setOwnJellyfin] = useState(false)
  const [status, setStatus] = useState<Status | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const lock = useRef(false)
  const [own, setOwn] = useState(false)
  const [apiId, setApiId] = useState("")
  const [apiHash, setApiHash] = useState("")
  const [signedIn, setSignedIn] = useState(false)
  const [folder, setFolder] = useState("")
  const [folderDefault, setFolderDefault] = useState("")
  const [folderInfo, setFolderInfo] = useState<{ free?: number; low?: boolean; exists?: boolean; error?: string } | null>(null)

  useEffect(() => {
    void api<{ available: boolean; signed_in: boolean; found?: Found }>("/setup/auto/info").then((info) => {
      setAvailable(info.available)
      setSignedIn(info.signed_in)
      if (info.found) {
        setFound(info.found); setMode("auto")
        if (languagePicked()) setStep("prefs")
        else resume.current = "prefs"
      }
    }).catch(() => setAvailable(false))
  }, [])

  useEffect(() => {
    void api<{ default: string }>("/setup/folder").then((f) => setFolderDefault(f.default)).catch(() => undefined)
  }, [])

  useEffect(() => {
    const path = (folder || folderDefault).trim()
    if (!path) return
    let stale = false
    const timer = setTimeout(() => {
      void api<{ free: number; low: boolean; exists: boolean }>("/setup/folder", { path })
        .then((f) => { if (!stale) setFolderInfo(f) })
        .catch((e) => { if (!stale) setFolderInfo({ error: e instanceof Error ? e.message : t("Something went wrong") }) })
    }, 500)
    return () => { stale = true; clearTimeout(timer) }
  }, [folder, folderDefault])

  useEffect(() => {
    if (step !== "bots" && step !== "done") return
    const id = setInterval(() => {
      if (step === "bots") void api<Job>("/setup/auto/job").then((j) => { setJob(j); if (j.owned?.length) setOwned(j.owned); if (j.found) { setFound(j.found); setStep("prefs") } })
      if (step === "done") void api<Status>("/setup/status").then(setStatus)
    }, 1000)
    return () => clearInterval(id)
  }, [step])

  function run(key: string, fn: () => Promise<void>) {
    if (lock.current) return
    lock.current = true
    setBusy(key)
    void fn().catch((e) => toast.error(e instanceof Error ? e.message : t("Something went wrong"))).finally(() => { lock.current = false; setBusy(null) })
  }

  async function save(auto: boolean) {
    if (mirrorOn && mirrorMode === "have" && !mirrorLink.trim()) throw new Error(t("Paste the public channel link, or choose automatic"))
    await api("/setup/save", {
      auto, ...manual, language: langs.join(","), ui_language: uiLang(), ...prefs, downloads: false,
      folder: folder.trim(),
      jellyfin_url: ownJellyfin ? prefs.jellyfin_url : "",
      jellyfin_key: ownJellyfin ? prefs.jellyfin_key : "",
      mirror: mirrorOn,
      mirror_title: mirrorOn && mirrorMode === "auto" ? mirror.title : "",
      mirror_username: mirrorOn && mirrorMode === "auto" ? mirror.username : "",
      mirror_channel: mirrorOn && mirrorMode === "have" ? mirrorLink.trim() : "",
    })
    setStep("done")
  }

  async function signOut() {
    if (lock.current) return
    if (!(await confirmDialog(t("Sign out of Telegram?"), t("The session on this computer ends. Sign in again to continue setup."), t("Sign out")))) return
    run("out", async () => {
      await api("/setup/auto/logout", {})
      setFound(null)
      setJob(null)
      setOwned([])
      setPicked([])
      setChannel("")
      setSignedIn(false)
      if (mode === "auto") setStep("login")
      toast(t("Signed out"))
    })
  }

  return (
    <div className="mx-auto max-w-lg space-y-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <div className="text-sm font-semibold">jellyfin-telegram <span className="font-normal text-muted-foreground">{t("First-time setup")}</span></div>
        {signedIn && step !== "done" && step !== "language" && <Button size="sm" variant="ghost" disabled={!!busy} loading={busy === "out"} onClick={() => void signOut()}>{t("Sign out")}</Button>}
      </div>
      {step !== "language" && <ol className="flex flex-wrap gap-x-2 text-xs text-muted-foreground">
        {(["Sign in", "Channel", "Bots", "Subtitles", "Filter", "Done"] as const).map((label, i) => <li key={label} className={i <= ["login", "channel", "bots", "prefs", "filter", "done"].indexOf(step) ? "font-medium text-foreground" : ""}>{i + 1}. {t(label)}</li>)}
      </ol>}
      <Card><CardBody className="space-y-3">
        {step === "language" && <>
          <h1 className="text-lg font-semibold">{t("Language")}</h1>
          <p className="text-sm text-muted-foreground">{t("The setup page and the panel use this language. You can change it later in the panel.")}</p>
          <div className="grid grid-cols-2 gap-2">
            {uiLanguages.map((item) => (
              <button key={item.code} type="button" className={"rounded-lg border p-3 text-start " + (uiLang() === item.code ? "border-2 border-primary bg-accent" : "hover:bg-accent")} onClick={() => setUiLang(item.code)}>
                <span className="text-sm font-semibold">{item.native}</span>
              </button>
            ))}
          </div>
          <div className="flex justify-end"><Button size="sm" onClick={() => { sessionStorage.setItem("jftg-ui-picked", "1"); if (!title) setTitle(t("Jellyfin library")); setStep(resume.current) }}>{t("Continue")}</Button></div>
        </>}
        {step !== "language" && mode === "choose" && <>
          <h1 className="text-lg font-semibold">{t("Connect Telegram")}</h1>
          <p className="text-sm text-muted-foreground">{t("Your videos live in a private Telegram channel, and bots read them for playback.")}</p>
          <Button className="w-full" disabled={!available || !!busy} loading={busy === "auto"} onClick={() => run("auto", async () => { const info = await api<{ signed_in?: boolean; found?: Found }>("/setup/auto/resume", {}); setMode("auto"); setSignedIn(!!info.signed_in); if (info.found) { setFound(info.found); setStep("prefs"); return }; setStep(info.signed_in ? "channel" : "login") })}>{t("Automatic")}</Button>
          <Button className="w-full" variant="outline" disabled={!!busy} onClick={() => { setMode("manual"); setStep("login") }}>{t("Manual")}</Button>
          {!available && <p className="text-sm">{t("The automatic setup is not available in this run. Use the manual way.")}</p>}
        </>}
        {mode === "auto" && step === "login" && <>
          <LoginForm resend path="/setup/auto/login" extra={own && apiId && apiHash ? { api_id: apiId, api_hash: apiHash } : undefined} onDone={() => { setSignedIn(true); setStep("channel") }} />
          <Checkbox checked={own} onCheckedChange={setOwn} label={t("Advanced: use my own api_id")} />
          {own && <div className="grid grid-cols-2 gap-2"><Input placeholder={t("API ID")} value={apiId} onChange={(e) => setApiId(e.target.value)} /><Input placeholder={t("API hash")} value={apiHash} onChange={(e) => setApiHash(e.target.value)} /></div>}
        </>}
        {mode === "auto" && step === "channel" && <>
          <h1 className="text-lg font-semibold">{t("Channel")}</h1>
          <p className="text-sm text-muted-foreground">{t("Use a channel this account already has, or make a new private one.")}</p>
          <div className="space-y-3 rounded-lg border-2 border-primary bg-accent/40 p-4">
            <div>
              <div className="text-sm font-semibold">{t("I already have a channel")}</div>
              <p className="mt-1 text-sm text-muted-foreground">{t("Paste a message link from the private library channel.")}</p>
            </div>
            <div className="space-y-2">
              <FieldLabel>{t("Channel link")}</FieldLabel>
              <Input disabled={!!busy} value={channel} placeholder="https://t.me/c/1234567890/5" onChange={(e) => setChannel(e.target.value)} />
            </div>
            <Button className="w-full" disabled={!!busy || !channel.trim()} loading={busy === "channel"} onClick={() => run("channel", async () => { const f = await api<Found>("/setup/auto/existing", { channel }); setFound(f); if (f.bots?.length) setStep("prefs"); else setStep("bots") })}>{t("Use this channel")}</Button>
          </div>
          <button type="button" disabled={!!busy} className="w-full rounded-lg border bg-card p-4 text-left shadow-sm hover:bg-accent disabled:opacity-50" onClick={() => { setChannel(""); setStep("bots") }}>
            <div className="text-sm font-semibold">{t("Create a channel automatically")}</div>
            <p className="mt-1 text-sm text-muted-foreground">{t("A new private channel and playback bots are made for this account.")}</p>
          </button>
        </>}
        {mode === "auto" && step === "bots" && <>
          <h1 className="text-lg font-semibold">{t("Bots")}</h1>
          {!job?.running && !owned.length && <>
            <FieldLabel>{t("Channel name")}</FieldLabel>
            <Input value={title} onChange={(e) => setTitle(e.target.value)} />
            <FieldLabel>{t("Number of bots")}</FieldLabel>
            <Input type="number" min={2} max={10} value={bots} onChange={(e) => setBots(Number(e.target.value))} />
            <p className="text-sm text-muted-foreground">{t("Five is the usual number. Two or more is enough if Telegram will not make all of them.")}</p>
            <div className="flex justify-end"><Button size="sm" disabled={!!busy || bots < 2 || bots > 10} loading={busy === "create"} onClick={() => run("create", () => api("/setup/auto/create", { title, channel, bots }))}>{t("Create channel and bots")}</Button></div>
          </>}
          <ProgressSteps lines={job?.progress ?? []} />
          {job?.error && <p className="text-sm text-destructive">{job.error}</p>}
          {owned.map((b) => <Checkbox key={b.username} label={"@" + b.username} checked={picked.includes(b.username)} onCheckedChange={(on) => setPicked(on ? [...picked, b.username] : picked.filter((n) => n !== b.username))} />)}
          {!!owned.length && <div className="flex justify-end"><Button size="sm" disabled={!!busy || !picked.length} loading={busy === "reuse"} onClick={() => run("reuse", async () => { await api("/setup/auto/reuse", { usernames: picked }); setOwned([]) })}>{t("Use the selected bots")}</Button></div>}
        </>}
        {mode === "manual" && step === "login" && <>
          <h1 className="text-lg font-semibold">{t("Telegram details")}</h1>
          <div className="grid grid-cols-2 gap-2"><Input placeholder={t("API ID")} value={manual.api_id} onChange={(e) => setManual({ ...manual, api_id: e.target.value })} /><Input placeholder={t("API hash")} value={manual.api_hash} onChange={(e) => setManual({ ...manual, api_hash: e.target.value })} /></div>
          <FieldLabel>{t("Your channel")}</FieldLabel>
          <Input value={manual.channel} placeholder="https://t.me/c/1234567890/5" onChange={(e) => setManual({ ...manual, channel: e.target.value })} />
          <FieldLabel>{t("Bot tokens")}</FieldLabel>
          <Textarea value={manual.bots} placeholder={t("One token per line")} onChange={(e) => setManual({ ...manual, bots: e.target.value })} />
          <div className="flex justify-end"><Button size="sm" disabled={!!busy} loading={busy === "verify"} onClick={() => run("verify", async () => { await api("/setup/verify", { ...manual, language: langs.join(","), auto: false }); setStep("prefs") })}>{t("Check with Telegram")}</Button></div>
        </>}
        {step === "prefs" && <>
          <h1 className="text-lg font-semibold">{t("Subtitles")}</h1>
          <p className="text-sm text-muted-foreground">{t("Tick every language you want kept. The first one is preferred.")}</p>
          <MultiSelect value={langs} onChange={setLangs} />
          {found && <p className="text-sm">{t("Channel {{title}}. Bots: {{bots}}.", { title: found.title, bots: found.bots.map((b) => "@" + b).join(", ") || t("none yet") })}</p>}
          <div className="space-y-3 border-t pt-3">
            <label className="flex items-center justify-between text-sm font-semibold">{t("Public mirror")} <SwitchBox label={t("Public mirror")} checked={mirrorOn} onCheckedChange={setMirrorOn} /></label>
            <p className="text-sm text-muted-foreground">{t("A second public channel gets a copy of films and series. On by default.")}</p>
            {mirrorOn && mirrorMode === "auto" && <>
              <div className="space-y-3 rounded-lg border-2 border-primary bg-accent/40 p-4">
                <div>
                  <div className="text-sm font-semibold">{t("Create automatically")}</div>
                  <p className="mt-1 text-sm text-muted-foreground">{t("A name and a username are already chosen. Change them if you want.")}</p>
                </div>
                <div className="space-y-2">
                  <FieldLabel>{t("Name")}</FieldLabel>
                  <Input value={mirror.title} onChange={(e) => setMirror({ ...mirror, title: e.target.value })} />
                  <FieldLabel>{t("Username")}</FieldLabel>
                  <Input value={mirror.username} onChange={(e) => setMirror({ ...mirror, username: e.target.value })} />
                </div>
              </div>
              <button type="button" disabled={!!busy} className="w-full rounded-lg border bg-card p-4 text-left shadow-sm hover:bg-accent disabled:opacity-50" onClick={() => setMirrorMode("have")}>
                <div className="text-sm font-semibold">{t("I already have a public channel")}</div>
                <p className="mt-1 text-sm text-muted-foreground">{t("Paste a link. Nothing new is created.")}</p>
              </button>
            </>}
            {mirrorOn && mirrorMode === "have" && <>
              <div className="space-y-3 rounded-lg border-2 border-primary bg-accent/40 p-4">
                <div>
                  <div className="text-sm font-semibold">{t("I already have a public channel")}</div>
                  <p className="mt-1 text-sm text-muted-foreground">{t("Paste a message link or @username. The bot is added as admin.")}</p>
                </div>
                <div className="space-y-2">
                  <FieldLabel>{t("Channel link")}</FieldLabel>
                  <Input value={mirrorLink} placeholder="https://t.me/films or @films" onChange={(e) => setMirrorLink(e.target.value)} />
                </div>
              </div>
              <button type="button" disabled={!!busy} className="w-full rounded-lg border bg-card p-4 text-left shadow-sm hover:bg-accent disabled:opacity-50" onClick={() => setMirrorMode("auto")}>
                <div className="text-sm font-semibold">{t("Create automatically")}</div>
                <p className="mt-1 text-sm text-muted-foreground">{t("A new public channel is made for this account.")}</p>
              </button>
            </>}
          </div>
          <label className="flex items-center justify-between text-sm">{t("Shared library")} <SwitchBox label={t("Shared library")} checked={prefs.group} onCheckedChange={(group) => setPrefs({ ...prefs, group })} /></label>
          <label className="flex items-center justify-between text-sm">{t("Telegram search")} <SwitchBox label={t("Telegram search")} checked={prefs.telegram_search} onCheckedChange={(telegram_search) => setPrefs({ ...prefs, telegram_search })} /></label>
          <label className="flex items-center justify-between text-sm">Fladder <SwitchBox label="Fladder" checked={prefs.fladder} onCheckedChange={(fladder) => setPrefs({ ...prefs, fladder })} /></label>
          <label className="flex items-center justify-between text-sm">Seerr <SwitchBox label="Seerr" checked={prefs.seerr} onCheckedChange={(seerr) => setPrefs({ ...prefs, seerr })} /></label>
          <div className="space-y-2 border-t pt-3">
            <div className="text-sm font-semibold">{t("Where to keep the files")}</div>
            <p className="text-sm text-muted-foreground">{t("One folder for Jellyfin, Seerr, the cache and the library. Pick a drive with enough free space.")}</p>
            <Input value={folder} placeholder={folderDefault} onChange={(e) => setFolder(e.target.value)} />
            {folderInfo?.error && <p className="text-sm text-destructive">{folderInfo.error}</p>}
            {folderInfo && !folderInfo.error && folderInfo.free !== undefined && (folderInfo.low
              ? <p className="text-sm text-destructive">{t("Only {{free}} free. At least 10 GB is recommended.", { free: bytes(folderInfo.free) })}</p>
              : <p className="text-sm text-muted-foreground">{t("{{free}} free", { free: bytes(folderInfo.free) })}{folderInfo.exists ? "" : " · " + t("The folder will be created.")}</p>)}
          </div>
          <div className="space-y-3 border-t pt-3">
            <div>
              <div className="text-sm font-semibold">Jellyfin</div>
              <p className="mt-1 text-sm text-muted-foreground">{t("A Jellyfin server is created for you.")}</p>
            </div>
            <button type="button" disabled={!!busy} className="w-full rounded-lg border bg-card p-4 text-left shadow-sm hover:bg-accent disabled:opacity-50" onClick={() => setOwnJellyfin((v) => !v)}>
              <div className="text-sm font-semibold">{t("I already run Jellyfin")}</div>
              <p className="mt-1 text-sm text-muted-foreground">{ownJellyfin ? t("The fields below are used instead of a new server.") : t("Click to enter the address and API key.")}</p>
            </button>
            {ownJellyfin && <>
              <FieldLabel>{t("Address")}</FieldLabel>
              <Input placeholder="http://127.0.0.1:8096" value={prefs.jellyfin_url} onChange={(e) => setPrefs({ ...prefs, jellyfin_url: e.target.value })} />
              <FieldLabel>{t("API key")}</FieldLabel>
              <Input placeholder={t("API key")} value={prefs.jellyfin_key} onChange={(e) => setPrefs({ ...prefs, jellyfin_key: e.target.value })} />
            </>}
          </div>
          <div className="flex justify-end"><Button size="sm" disabled={!!busy || !!folderInfo?.error} onClick={() => setStep("filter")}>{t("Continue")}</Button></div>
        </>}
        {step === "filter" && <>
          <h1 className="text-lg font-semibold">{t("Auto-add filter")}</h1>
          <p className="text-sm text-muted-foreground">{t("Choose which trending titles are added to the library by themselves. Skip to set this later in the panel.")}</p>
          <AutoFilterForm path="/setup/auto-filter" saveLabel={t("Save and install")} onSaved={() => save(mode === "auto")} extra={<>
            <Button type="button" size="sm" variant="ghost" disabled={!!busy} onClick={() => setStep("prefs")}>{t("Back")}</Button>
            <Button type="button" size="sm" variant="outline" disabled={!!busy} loading={busy === "save"} onClick={() => run("save", () => save(mode === "auto"))}>{t("Skip")}</Button>
          </>} />
        </>}
        {step === "done" && status?.ready && !status.error && <>
          <h1 className="text-lg font-semibold">{t("Installed")}</h1>
          <p className="text-sm text-muted-foreground">{t("Setup finished. Open the panel to use this server.")}</p>
          <Button className="h-12 w-full text-base" asChild>
            <a href={"/panel" + (panelCode() ? "?code=" + encodeURIComponent(panelCode()) : "")}>{t("Open panel")}</a>
          </Button>
        </>}
        {step === "done" && !(status?.ready && !status.error) && <>
          <h1 className="text-lg font-semibold">{status?.stage || t("Installing")}</h1>
          <div className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full bg-primary" style={{ width: (status?.percent ?? 0) + "%" }} /></div>
          {status?.error && <p className="text-sm text-destructive">{status.error}</p>}
        </>}
      </CardBody></Card>
    </div>
  )
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <I18nProvider>
        <Toaster position={document.documentElement.dir === "rtl" ? "bottom-left" : "bottom-right"} />
        <ConfirmHost />
        <Setup />
      </I18nProvider>
    </QueryClientProvider>
  </StrictMode>,
)
