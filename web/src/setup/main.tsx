import { StrictMode, useEffect, useRef, useState } from "react"
import { createRoot } from "react-dom/client"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster, toast } from "sonner"
import "@/index.css"
import { api, panelCode } from "@/lib/api"
import { Button, Card, CardBody, Checkbox, FieldLabel, Input, SwitchBox, Textarea } from "@/components/ui"
import { ConfirmHost, confirmDialog, CopyField, LoginForm, MultiSelect, ProgressSteps } from "@/components/widgets"

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

function Setup() {
  const [mode, setMode] = useState<"choose" | "auto" | "manual">("choose")
  const [step, setStep] = useState<"login" | "channel" | "bots" | "prefs" | "done">("login")
  const [available, setAvailable] = useState(true)
  const [found, setFound] = useState<Found | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  const [owned, setOwned] = useState<{ username: string; name?: string }[]>([])
  const [picked, setPicked] = useState<string[]>([])
  const [langs, setLangs] = useState<string[]>([])
  const [title, setTitle] = useState("Jellyfin library")
  const [bots, setBots] = useState(3)
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

  useEffect(() => {
    void api<{ available: boolean; signed_in: boolean; found?: Found }>("/setup/auto/info").then((info) => {
      setAvailable(info.available)
      setSignedIn(info.signed_in)
      if (info.found) { setFound(info.found); setMode("auto"); setStep("prefs") }
    }).catch(() => setAvailable(false))
  }, [])

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
    void fn().catch((e) => toast.error(e instanceof Error ? e.message : "Something went wrong")).finally(() => { lock.current = false; setBusy(null) })
  }

  async function save(auto: boolean) {
    if (mirrorOn && mirrorMode === "have" && !mirrorLink.trim()) throw new Error("Paste the public channel link, or choose automatic")
    await api("/setup/save", {
      auto, ...manual, language: langs.join(","), ...prefs,
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
    if (!(await confirmDialog("Sign out of Telegram?", "The session on this computer ends. Sign in again to continue setup.", "Sign out"))) return
    run("out", async () => {
      await api("/setup/auto/logout", {})
      setFound(null)
      setJob(null)
      setOwned([])
      setPicked([])
      setChannel("")
      setSignedIn(false)
      if (mode === "auto") setStep("login")
      toast("Signed out")
    })
  }

  return (
    <div className="mx-auto max-w-lg space-y-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <div className="text-sm font-semibold">jellyfin-telegram <span className="font-normal text-muted-foreground">First-time setup</span></div>
        {signedIn && step !== "done" && <Button size="sm" variant="ghost" disabled={!!busy} loading={busy === "out"} onClick={() => void signOut()}>Sign out</Button>}
      </div>
      <ol className="flex gap-2 text-xs text-muted-foreground">
        {["Sign in", "Channel", "Bots", "Subtitles", "Done"].map((label, i) => <li key={label} className={i <= ["login", "channel", "bots", "prefs", "done"].indexOf(step) ? "font-medium text-foreground" : ""}>{i + 1}. {label}</li>)}
      </ol>
      <Card><CardBody className="space-y-3">
        {mode === "choose" && <>
          <h1 className="text-lg font-semibold">Connect Telegram</h1>
          <p className="text-sm text-muted-foreground">Your videos live in a private Telegram channel, and bots read them for playback.</p>
          <Button className="w-full" disabled={!available || !!busy} loading={busy === "auto"} onClick={() => run("auto", async () => { const info = await api<{ signed_in?: boolean; found?: Found }>("/setup/auto/resume", {}); setMode("auto"); setSignedIn(!!info.signed_in); if (info.found) { setFound(info.found); setStep("prefs"); return }; setStep(info.signed_in ? "channel" : "login") })}>Automatic</Button>
          <Button className="w-full" variant="outline" disabled={!!busy} onClick={() => { setMode("manual"); setStep("login") }}>Manual</Button>
          {!available && <p className="text-sm">The automatic setup is not available in this run. Use the manual way.</p>}
        </>}
        {mode === "auto" && step === "login" && <>
          <LoginForm resend path="/setup/auto/login" extra={own && apiId && apiHash ? { api_id: apiId, api_hash: apiHash } : undefined} onDone={() => { setSignedIn(true); setStep("channel") }} />
          <Checkbox checked={own} onCheckedChange={setOwn} label="Advanced: use my own api_id" />
          {own && <div className="grid grid-cols-2 gap-2"><Input placeholder="API ID" value={apiId} onChange={(e) => setApiId(e.target.value)} /><Input placeholder="API hash" value={apiHash} onChange={(e) => setApiHash(e.target.value)} /></div>}
        </>}
        {mode === "auto" && step === "channel" && <>
          <h1 className="text-lg font-semibold">Channel</h1>
          <p className="text-sm text-muted-foreground">Use a channel this account already has, or make a new private one.</p>
          <div className="space-y-3 rounded-lg border-2 border-primary bg-accent/40 p-4">
            <div>
              <div className="text-sm font-semibold">I already have a channel</div>
              <p className="mt-1 text-sm text-muted-foreground">Paste a message link from the private library channel.</p>
            </div>
            <div className="space-y-2">
              <FieldLabel>Channel link</FieldLabel>
              <Input disabled={!!busy} value={channel} placeholder="https://t.me/c/1234567890/5" onChange={(e) => setChannel(e.target.value)} />
            </div>
            <Button className="w-full" disabled={!!busy || !channel.trim()} loading={busy === "channel"} onClick={() => run("channel", async () => { const f = await api<Found>("/setup/auto/existing", { channel }); setFound(f); if (f.bots?.length) setStep("prefs"); else setStep("bots") })}>Use this channel</Button>
          </div>
          <button type="button" disabled={!!busy} className="w-full rounded-lg border bg-card p-4 text-left shadow-sm hover:bg-accent disabled:opacity-50" onClick={() => { setChannel(""); setStep("bots") }}>
            <div className="text-sm font-semibold">Create a channel automatically</div>
            <p className="mt-1 text-sm text-muted-foreground">A new private channel and playback bots are made for this account.</p>
          </button>
        </>}
        {mode === "auto" && step === "bots" && <>
          <h1 className="text-lg font-semibold">Bots</h1>
          {!job?.running && !owned.length && <>
            <FieldLabel>Channel name</FieldLabel>
            <Input value={title} onChange={(e) => setTitle(e.target.value)} />
            <FieldLabel>Number of bots</FieldLabel>
            <Input type="number" min={1} max={10} value={bots} onChange={(e) => setBots(Number(e.target.value))} />
            <div className="flex justify-end"><Button size="sm" disabled={!!busy} loading={busy === "create"} onClick={() => run("create", () => api("/setup/auto/create", { title, channel, bots }))}>Create channel and bots</Button></div>
          </>}
          <ProgressSteps lines={job?.progress ?? []} />
          {job?.error && <p className="text-sm text-destructive">{job.error}</p>}
          {owned.map((b) => <Checkbox key={b.username} label={"@" + b.username} checked={picked.includes(b.username)} onCheckedChange={(on) => setPicked(on ? [...picked, b.username] : picked.filter((n) => n !== b.username))} />)}
          {!!owned.length && <div className="flex justify-end"><Button size="sm" disabled={!!busy || !picked.length} loading={busy === "reuse"} onClick={() => run("reuse", async () => { await api("/setup/auto/reuse", { usernames: picked }); setOwned([]) })}>Use the selected bots</Button></div>}
        </>}
        {mode === "manual" && step === "login" && <>
          <h1 className="text-lg font-semibold">Telegram details</h1>
          <div className="grid grid-cols-2 gap-2"><Input placeholder="API ID" value={manual.api_id} onChange={(e) => setManual({ ...manual, api_id: e.target.value })} /><Input placeholder="API hash" value={manual.api_hash} onChange={(e) => setManual({ ...manual, api_hash: e.target.value })} /></div>
          <FieldLabel>Your channel</FieldLabel>
          <Input value={manual.channel} placeholder="https://t.me/c/1234567890/5" onChange={(e) => setManual({ ...manual, channel: e.target.value })} />
          <FieldLabel>Bot tokens</FieldLabel>
          <Textarea value={manual.bots} placeholder="One token per line" onChange={(e) => setManual({ ...manual, bots: e.target.value })} />
          <div className="flex justify-end"><Button size="sm" disabled={!!busy} loading={busy === "verify"} onClick={() => run("verify", async () => { await api("/setup/verify", { ...manual, language: langs.join(","), auto: false }); setStep("prefs") })}>Check with Telegram</Button></div>
        </>}
        {step === "prefs" && <>
          <h1 className="text-lg font-semibold">Subtitles</h1>
          <p className="text-sm text-muted-foreground">Tick every language you want kept. The first one is preferred.</p>
          <MultiSelect value={langs} onChange={setLangs} />
          {found && <p className="text-sm">Channel {found.title}. Bots: {found.bots.map((b) => "@" + b).join(", ") || "none yet"}.</p>}
          <div className="space-y-3 border-t pt-3">
            <label className="flex items-center justify-between text-sm font-semibold">Public mirror <SwitchBox label="Public mirror" checked={mirrorOn} onCheckedChange={setMirrorOn} /></label>
            <p className="text-sm text-muted-foreground">A second public channel gets a copy of films and series. On by default.</p>
            {mirrorOn && mirrorMode === "auto" && <>
              <div className="space-y-3 rounded-lg border-2 border-primary bg-accent/40 p-4">
                <div>
                  <div className="text-sm font-semibold">Create automatically</div>
                  <p className="mt-1 text-sm text-muted-foreground">A name and a username are already chosen. Change them if you want.</p>
                </div>
                <div className="space-y-2">
                  <FieldLabel>Name</FieldLabel>
                  <Input value={mirror.title} onChange={(e) => setMirror({ ...mirror, title: e.target.value })} />
                  <FieldLabel>Username</FieldLabel>
                  <Input value={mirror.username} onChange={(e) => setMirror({ ...mirror, username: e.target.value })} />
                </div>
              </div>
              <button type="button" disabled={!!busy} className="w-full rounded-lg border bg-card p-4 text-left shadow-sm hover:bg-accent disabled:opacity-50" onClick={() => setMirrorMode("have")}>
                <div className="text-sm font-semibold">I already have a public channel</div>
                <p className="mt-1 text-sm text-muted-foreground">Paste a link. Nothing new is created.</p>
              </button>
            </>}
            {mirrorOn && mirrorMode === "have" && <>
              <div className="space-y-3 rounded-lg border-2 border-primary bg-accent/40 p-4">
                <div>
                  <div className="text-sm font-semibold">I already have a public channel</div>
                  <p className="mt-1 text-sm text-muted-foreground">Paste a message link or @username. The bot is added as admin.</p>
                </div>
                <div className="space-y-2">
                  <FieldLabel>Channel link</FieldLabel>
                  <Input value={mirrorLink} placeholder="https://t.me/films or @films" onChange={(e) => setMirrorLink(e.target.value)} />
                </div>
              </div>
              <button type="button" disabled={!!busy} className="w-full rounded-lg border bg-card p-4 text-left shadow-sm hover:bg-accent disabled:opacity-50" onClick={() => setMirrorMode("auto")}>
                <div className="text-sm font-semibold">Create automatically</div>
                <p className="mt-1 text-sm text-muted-foreground">A new public channel is made for this account.</p>
              </button>
            </>}
          </div>
          <label className="flex items-center justify-between text-sm">Shared library <SwitchBox label="Shared library" checked={prefs.group} onCheckedChange={(group) => setPrefs({ ...prefs, group })} /></label>
          <label className="flex items-center justify-between text-sm">Telegram search <SwitchBox label="Telegram search" checked={prefs.telegram_search} onCheckedChange={(telegram_search) => setPrefs({ ...prefs, telegram_search })} /></label>
          <label className="flex items-center justify-between text-sm">Fladder <SwitchBox label="Fladder" checked={prefs.fladder} onCheckedChange={(fladder) => setPrefs({ ...prefs, fladder })} /></label>
          <label className="flex items-center justify-between text-sm">Seerr <SwitchBox label="Seerr" checked={prefs.seerr} onCheckedChange={(seerr) => setPrefs({ ...prefs, seerr })} /></label>
          <label className="flex items-center justify-between text-sm">Torrent <SwitchBox label="Torrent" checked={prefs.downloads} onCheckedChange={(downloads) => setPrefs({ ...prefs, downloads })} /></label>
          {prefs.downloads && <p className="text-sm text-destructive">Torrents connect this computer to strangers and can be illegal where you live.</p>}
          <div className="space-y-3 border-t pt-3">
            <div>
              <div className="text-sm font-semibold">Jellyfin</div>
              <p className="mt-1 text-sm text-muted-foreground">A Jellyfin server is created for you.</p>
            </div>
            <button type="button" disabled={!!busy} className="w-full rounded-lg border bg-card p-4 text-left shadow-sm hover:bg-accent disabled:opacity-50" onClick={() => setOwnJellyfin((v) => !v)}>
              <div className="text-sm font-semibold">I already run Jellyfin</div>
              <p className="mt-1 text-sm text-muted-foreground">{ownJellyfin ? "The fields below are used instead of a new server." : "Click to enter the address and API key."}</p>
            </button>
            {ownJellyfin && <>
              <FieldLabel>Address</FieldLabel>
              <Input placeholder="http://127.0.0.1:8096" value={prefs.jellyfin_url} onChange={(e) => setPrefs({ ...prefs, jellyfin_url: e.target.value })} />
              <FieldLabel>API key</FieldLabel>
              <Input placeholder="API key" value={prefs.jellyfin_key} onChange={(e) => setPrefs({ ...prefs, jellyfin_key: e.target.value })} />
            </>}
          </div>
          <div className="flex justify-end"><Button size="sm" disabled={!!busy} loading={busy === "save"} onClick={() => run("save", () => save(mode === "auto"))}>Save and install</Button></div>
        </>}
        {step === "done" && <>
          <h1 className="text-lg font-semibold">{status?.ready ? "Ready" : (status?.stage || "Installing")}</h1>
          <div className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full bg-primary" style={{ width: (status?.percent ?? 0) + "%" }} /></div>
          {status?.error && <p className="text-sm text-destructive">{status.error}</p>}
          {status?.user && <CopyField label="User" value={status.user} />}
          {status?.password && <CopyField label="Password" value={status.password} />}
          {(status?.urls ?? []).map((u) => <a key={u} className="block text-sm underline" href={u}>{u}</a>)}
          <p className="text-xs text-muted-foreground">Code {panelCode()}</p>
        </>}
      </CardBody></Card>
    </div>
  )
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <Toaster position="bottom-right" />
      <ConfirmHost />
      <Setup />
    </QueryClientProvider>
  </StrictMode>,
)
