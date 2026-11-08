import { StrictMode, useEffect, useState } from "react"
import { createRoot } from "react-dom/client"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster, toast } from "sonner"
import "@/index.css"
import { api, panelCode } from "@/lib/api"
import { Button, Card, CardBody, Checkbox, FieldLabel, Input, SwitchBox, Textarea } from "@/components/ui"
import { CopyField, LoginForm, MultiSelect, ProgressSteps } from "@/components/widgets"

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
  const [mirrorOn, setMirrorOn] = useState(false)
  const [mirror, setMirror] = useState(randomMirror)
  const [advanced, setAdvanced] = useState(false)
  const [status, setStatus] = useState<Status | null>(null)
  const [own, setOwn] = useState(false)
  const [apiId, setApiId] = useState("")
  const [apiHash, setApiHash] = useState("")

  useEffect(() => {
    void api<{ available: boolean; signed_in: boolean; found?: Found }>("/setup/auto/info").then((info) => {
      setAvailable(info.available)
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

  async function save(auto: boolean) {
    await api("/setup/save", {
      auto, ...manual, language: langs.join(","), ...prefs,
      mirror: mirrorOn, mirror_title: mirror.title, mirror_username: mirror.username,
    })
    setStep("done")
  }

  return (
    <div className="mx-auto max-w-lg space-y-4 p-4">
      <div className="text-sm font-semibold">jellyfin-telegram <span className="font-normal text-muted-foreground">First-time setup</span></div>
      <ol className="flex gap-2 text-xs text-muted-foreground">
        {["Sign in", "Channel", "Bots", "Subtitles", "Done"].map((label, i) => <li key={label} className={i <= ["login", "channel", "bots", "prefs", "done"].indexOf(step) ? "font-medium text-foreground" : ""}>{i + 1}. {label}</li>)}
      </ol>
      <Card><CardBody className="space-y-3">
        {mode === "choose" && <>
          <h1 className="text-lg font-semibold">Connect Telegram</h1>
          <p className="text-sm text-muted-foreground">Your videos live in a private Telegram channel, and bots read them for playback.</p>
          <Button className="w-full" disabled={!available} onClick={() => { setMode("auto"); void api("/setup/auto/resume", {}).then((r) => { const info = r as { signed_in?: boolean; found?: Found }; if (info.found) { setFound(info.found); setStep("prefs"); return }; setStep(info.signed_in ? "channel" : "login") }).catch((e) => toast.error(e.message)) }}>Automatic</Button>
          <Button className="w-full" variant="outline" onClick={() => { setMode("manual"); setStep("login") }}>Manual</Button>
          {!available && <p className="text-sm">The automatic setup is not available in this run. Use the manual way.</p>}
        </>}
        {mode === "auto" && step === "login" && <>
          <LoginForm resend path="/setup/auto/login" extra={own && apiId && apiHash ? { api_id: apiId, api_hash: apiHash } : undefined} onDone={() => setStep("channel")} />
          <Checkbox checked={own} onCheckedChange={setOwn} label="Advanced: use my own api_id" />
          {own && <div className="grid grid-cols-2 gap-2"><Input placeholder="API ID" value={apiId} onChange={(e) => setApiId(e.target.value)} /><Input placeholder="API hash" value={apiHash} onChange={(e) => setApiHash(e.target.value)} /></div>}
        </>}
        {mode === "auto" && step === "channel" && <>
          <h1 className="text-lg font-semibold">Channel</h1>
          <p className="text-sm text-muted-foreground">Have you set up a library channel with this account before?</p>
          <FieldLabel>Existing channel</FieldLabel>
          <Input value={channel} placeholder="https://t.me/c/1234567890/5" onChange={(e) => setChannel(e.target.value)} />
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="outline" onClick={() => api<Found>("/setup/auto/existing", { channel }).then((f) => { setFound(f); if (f.bots?.length) setStep("prefs"); else setStep("bots") }).catch((e) => toast.error(e.message))}>Find existing</Button>
            <Button size="sm" onClick={() => setStep("bots")}>Create</Button>
          </div>
        </>}
        {mode === "auto" && step === "bots" && <>
          <h1 className="text-lg font-semibold">Bots</h1>
          {!job?.running && !owned.length && <>
            <FieldLabel>Channel name</FieldLabel>
            <Input value={title} onChange={(e) => setTitle(e.target.value)} />
            <FieldLabel>Number of bots</FieldLabel>
            <Input type="number" min={1} max={10} value={bots} onChange={(e) => setBots(Number(e.target.value))} />
            <div className="flex justify-end"><Button size="sm" onClick={() => api("/setup/auto/create", { title, channel, bots }).catch((e) => toast.error(e.message))}>Create channel and bots</Button></div>
          </>}
          <ProgressSteps lines={job?.progress ?? []} />
          {job?.error && <p className="text-sm text-destructive">{job.error}</p>}
          {owned.map((b) => <Checkbox key={b.username} label={"@" + b.username} checked={picked.includes(b.username)} onCheckedChange={(on) => setPicked(on ? [...picked, b.username] : picked.filter((n) => n !== b.username))} />)}
          {!!owned.length && <div className="flex justify-end"><Button size="sm" onClick={() => api("/setup/auto/reuse", { usernames: picked }).then(() => setOwned([])).catch((e) => toast.error(e.message))}>Use the selected bots</Button></div>}
        </>}
        {mode === "manual" && step === "login" && <>
          <h1 className="text-lg font-semibold">Telegram details</h1>
          <div className="grid grid-cols-2 gap-2"><Input placeholder="API ID" value={manual.api_id} onChange={(e) => setManual({ ...manual, api_id: e.target.value })} /><Input placeholder="API hash" value={manual.api_hash} onChange={(e) => setManual({ ...manual, api_hash: e.target.value })} /></div>
          <FieldLabel>Your channel</FieldLabel>
          <Input value={manual.channel} placeholder="https://t.me/c/1234567890/5" onChange={(e) => setManual({ ...manual, channel: e.target.value })} />
          <FieldLabel>Bot tokens</FieldLabel>
          <Textarea value={manual.bots} placeholder="One token per line" onChange={(e) => setManual({ ...manual, bots: e.target.value })} />
          <div className="flex justify-end"><Button size="sm" onClick={() => api("/setup/verify", { ...manual, language: langs.join(","), auto: false }).then(() => setStep("prefs")).catch((e) => toast.error(e.message))}>Check with Telegram</Button></div>
        </>}
        {step === "prefs" && <>
          <h1 className="text-lg font-semibold">Subtitles</h1>
          <p className="text-sm text-muted-foreground">Tick every language you want kept. The first one is preferred.</p>
          <MultiSelect value={langs} onChange={setLangs} />
          {found && <p className="text-sm">Channel {found.title}. Bots: {found.bots.map((b) => "@" + b).join(", ") || "none yet"}.</p>}
          <label className="flex items-center justify-between text-sm">Public mirror <SwitchBox label="Public mirror" checked={mirrorOn} onCheckedChange={setMirrorOn} /></label>
          <p className="text-sm text-muted-foreground">A second, public channel that gets a copy of films and series. Off by default. A name and a username are chosen for you.</p>
          {mirrorOn && <button type="button" className="text-left text-sm underline" onClick={() => setAdvanced((v) => !v)}>Choose the name and username</button>}
          {mirrorOn && advanced && <>
            <FieldLabel>Name</FieldLabel>
            <Input value={mirror.title} onChange={(e) => setMirror({ ...mirror, title: e.target.value })} />
            <FieldLabel>Username</FieldLabel>
            <Input value={mirror.username} onChange={(e) => setMirror({ ...mirror, username: e.target.value })} />
          </>}
          <label className="flex items-center justify-between text-sm">Shared library <SwitchBox label="Shared library" checked={prefs.group} onCheckedChange={(group) => setPrefs({ ...prefs, group })} /></label>
          <label className="flex items-center justify-between text-sm">Telegram search <SwitchBox label="Telegram search" checked={prefs.telegram_search} onCheckedChange={(telegram_search) => setPrefs({ ...prefs, telegram_search })} /></label>
          <label className="flex items-center justify-between text-sm">Fladder <SwitchBox label="Fladder" checked={prefs.fladder} onCheckedChange={(fladder) => setPrefs({ ...prefs, fladder })} /></label>
          <label className="flex items-center justify-between text-sm">Seerr <SwitchBox label="Seerr" checked={prefs.seerr} onCheckedChange={(seerr) => setPrefs({ ...prefs, seerr })} /></label>
          <label className="flex items-center justify-between text-sm">Torrent <SwitchBox label="Torrent" checked={prefs.downloads} onCheckedChange={(downloads) => setPrefs({ ...prefs, downloads })} /></label>
          {prefs.downloads && <p className="text-sm text-destructive">Torrents connect this computer to strangers and can be illegal where you live.</p>}
          <FieldLabel>I already run Jellyfin</FieldLabel>
          <Input placeholder="http://127.0.0.1:8096" value={prefs.jellyfin_url} onChange={(e) => setPrefs({ ...prefs, jellyfin_url: e.target.value })} />
          <Input placeholder="API key" value={prefs.jellyfin_key} onChange={(e) => setPrefs({ ...prefs, jellyfin_key: e.target.value })} />
          <div className="flex justify-end"><Button size="sm" onClick={() => save(mode === "auto").catch((e) => toast.error(e.message))}>Save and install</Button></div>
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
      <Setup />
    </QueryClientProvider>
  </StrictMode>,
)
