import { useEffect, useState, type ReactNode } from "react"
import { Link } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api, panelCode, pollingPaused } from "@/lib/api"
import { bytes } from "@/lib/utils"
import { Button, Card, CardBody, Collapse, FieldLabel, Input, SwitchBox, Toggles } from "@/components/ui"
import { confirmDialog, CopyField, MultiSelect, ProgressSteps, StatusBadge, TelegramLoginDialog } from "@/components/widgets"
import { useJob, usePanel, type PanelState, type Session } from "@/panel/shell"

function Page({ title, text, children }: { title: string; text: string; children: ReactNode }) {
  return <div className="space-y-4"><div><h1 className="text-xl font-semibold">{title}</h1><p className="text-sm text-muted-foreground">{text}</p></div>{children}</div>
}

function useDirty<T>(remote: T) {
  const [value, setValue] = useState(remote)
  const [dirty, setDirty] = useState(false)
  const key = JSON.stringify(remote)
  useEffect(() => { if (!dirty) setValue(remote) }, [key, dirty])
  return { value, set: (v: T) => { setDirty(true); setValue(v) }, saved: () => setDirty(false) }
}

async function run(path: string, body: unknown, ok: string) {
  await api(path, body)
  toast(ok)
}

export function Overview() {
  const { data: s } = usePanel()
  if (!s) return null
  const signed = (s.telegram?.sessions?.length ?? 0) > 0
  const botsOk = s.bots.filter((b) => b.ok && b.admin).length
  const phone = s.addresses.find((a) => !a.away && !/localhost|127\.0\.0\.1/.test(a.url))
  const share = phone ? phone.url.replace(/\/$/, "") + "/panel?code=" + panelCode() : ""
  const pass = s.accounts.some((a) => a.app === "Jellyfin" || a.app === "Seerr")
  const forum = s.forum as { error?: string; fingerprint?: string; group?: boolean } | null
  const dl = s.downloads as { enabled?: boolean; queue?: unknown[] } | null
  return (
    <Page title="Overview" text="Everything this server runs, and where to open it.">
      {pass && <div className="rounded-md border bg-card px-3 py-2 text-sm">Don't know your Jellyfin or Seerr password? <Link className="underline" to="/accounts">Sign in details</Link></div>}
      <div className="grid gap-2 sm:grid-cols-3">
        <a className="rounded-lg border bg-card p-3" href="/"><strong>Jellyfin</strong><div className="text-sm text-muted-foreground">Watch your library</div></a>
        {s.seerr && <a className="rounded-lg border bg-card p-3" href="/seerr/"><strong>Seerr</strong><div className="text-sm text-muted-foreground">Request films and series</div></a>}
        {s.fladder && <a className="rounded-lg border bg-card p-3" href="/fladder/"><strong>Fladder</strong><div className="text-sm text-muted-foreground">A nicer web client</div></a>}
      </div>
      <div className="grid gap-2 sm:grid-cols-4">
        <Stat label="Bots" value={String(s.bots.length)} sub={botsOk + " ready"} />
        <Stat label="Channel" value={"-100" + s.channel_id} sub={s.language || "No subtitles"} />
        <Stat label="Shared library" value={forum?.error ? "Problem" : forum?.fingerprint ? "On" : forum?.group ? "Waiting" : "Off"} sub={forum?.fingerprint ? "Key " + forum.fingerprint : "Plain channel"} />
        {s.downloads && <Stat label="Downloads" value={dl?.enabled ? "On" : "Off"} sub={dl?.enabled ? (dl.queue?.length ?? 0) + " in the queue" : "Files in the channel still play"} />}
      </div>
      <Card><CardBody className="space-y-2">
        <h2 className="font-medium">Open from another device</h2>
        <p className="text-sm text-muted-foreground">On this computer, open /panel. From a phone on the same Wi-Fi, open this link. The part after code= is the secret that keeps the panel private.</p>
        <CopyField label="Share link" value={share || "No network address yet"} />
        {s.fladder && <a className="text-sm underline" href="https://github.com/DonutWare/Fladder/releases">Download other Fladder versions</a>}
        {!signed && <p className="text-sm">No Telegram account is signed in. <Link className="underline" to="/telegram">Accounts</Link></p>}
      </CardBody></Card>
    </Page>
  )
}

function Stat({ label, value, sub }: { label: string; value: string; sub: string }) {
  return <div className="rounded-lg border bg-card p-3"><div className="text-xs text-muted-foreground">{label}</div><div className="truncate text-lg font-semibold">{value}</div><div className="text-xs text-muted-foreground">{sub}</div></div>
}

export function Accounts() {
  const { data: s } = usePanel()
  return (
    <Page title="Sign in details" text="The account to use in each app. Share this computer's address with the people who should watch, and send them to Jellyfin first.">
      <Card><CardBody className="space-y-3">
        {(s?.accounts ?? []).map((a) => (
          <div key={a.app} className="flex flex-wrap items-center justify-between gap-2 border-b pb-2 last:border-0">
            <div><div className="font-medium">{a.app}</div><div className="text-xs text-muted-foreground">{a.path}{a.note ? " · " + a.note : ""}</div></div>
            <div className="flex gap-3">{a.user && <CopyField label="User" value={a.user} />}{a.password && <CopyField label="Password" value={a.password} />}</div>
          </div>
        ))}
        {!s?.accounts?.length && <p className="text-sm text-muted-foreground">No saved sign-in yet.</p>}
      </CardBody></Card>
    </Page>
  )
}

export function Channel() {
  const { data: s } = usePanel()
  const qc = useQueryClient()
  const channel = useDirty(s ? "-100" + s.channel_id : "")
  const search = s?.telegram_search !== false
  if (!s) return null
  return (
    <Page title="Main channel" text="The channel that holds the library. Replacing it is refused once files are indexed, because the library would point at the wrong messages.">
      <Card><CardBody className="space-y-3">
        <div className="text-sm">Current channel <code>-100{s.channel_id}</code></div>
        <FieldLabel>Replace channel</FieldLabel>
        <Input value={channel.value} onChange={(e) => channel.set(e.target.value)} />
        <div className="flex justify-end"><Button size="sm" onClick={() => run("/panel/settings", { channel: channel.value }, "Saved").then(() => { channel.saved(); void qc.invalidateQueries({ queryKey: ["panel-state"] }) }).catch((e) => toast.error(e.message))}>Replace</Button></div>
      </CardBody></Card>
      <Card><CardBody className="flex items-center justify-between gap-3">
        <div><div className="font-medium">Telegram search</div><div className="text-sm text-muted-foreground">Look up a requested title on Telegram and copy a file that is found.</div></div>
        <SwitchBox label="Telegram search" checked={search} onCheckedChange={(on) => { void api("/panel/search", { on }).then(() => qc.invalidateQueries({ queryKey: ["panel-state"] })).catch((e) => toast.error(e.message)) }} />
      </CardBody></Card>
    </Page>
  )
}

const kinds = [["movie", "Films"], ["series", "Series"], ["music", "Music"], ["video", "General video"]] as const

export function Mirrors() {
  const { data: s } = usePanel()
  const job = useJob()
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [title, setTitle] = useState("")
  const [pub, setPub] = useState(true)
  const [user, setUser] = useState("")
  const [pick, setPick] = useState<string[]>(["movie", "series"])
  const [bot, setBot] = useState("")
  const [existing, setExisting] = useState("")
  const [exBot, setExBot] = useState("")
  const [exKinds, setExKinds] = useState<string[]>(["movie", "series"])
  if (!s) return null
  const bots = s.bots.filter((b) => b.connected)
  const chosen = bot || (bots[0] ? String(bots[0].id) : "new")
  async function refresh() { await qc.invalidateQueries({ queryKey: ["panel-state"] }) }
  return (
    <Page title="Mirrors" text="Each mirror is another channel. Choose which parts are copied there, and which bot posts them.">
      <Card><CardBody className="space-y-3">
        <h2 className="font-medium">Create mirror</h2>
        {!open && <Button size="sm" onClick={() => setOpen(true)}>Create mirror</Button>}
        {open && <div className="space-y-2">
          <Input value={title} placeholder="Channel name" onChange={(e) => setTitle(e.target.value)} />
          <label className="flex items-center justify-between text-sm">Public <SwitchBox label="Public" checked={pub} onCheckedChange={setPub} /></label>
          {pub && <Input value={user} placeholder="Username, optional" onChange={(e) => setUser(e.target.value)} />}
          <KindPicks value={pick} onChange={setPick} />
          <select className="h-9 w-full rounded-md border bg-card px-2 text-sm" value={chosen} onChange={(e) => setBot(e.target.value)}>
            <option value="new">Create a new bot</option>
            {bots.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}
          </select>
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>Cancel</Button>
            <Button size="sm" onClick={() => api("/panel/mirrors/create", { title, public: pub, username: user, kinds: pick, bot: chosen === "new" ? "new" : Number(chosen) }).then(() => toast("Creating the mirror")).catch((e) => toast.error(e.message))}>Create</Button>
          </div>
        </div>}
        <ProgressSteps lines={job.data?.progress ?? []} />
        {job.data?.error && <p className="text-sm text-destructive">{job.data.error}</p>}
        {job.data?.result?.link && <CopyField label="Public link" value={job.data.result.link} />}
      </CardBody></Card>
      <Card><CardBody className="space-y-2">
        {(s.mirrors ?? []).map((m) => <MirrorRow key={m.id} m={m} bots={s.bots} onDone={refresh} />)}
        {!s.mirrors?.length && <p className="text-sm text-muted-foreground">No mirror yet.</p>}
      </CardBody></Card>
      <Card><CardBody className="space-y-2">
        <h2 className="font-medium">Add existing channel</h2>
        <Input value={existing} placeholder="https://t.me/c/1234567890/5 or @films" onChange={(e) => setExisting(e.target.value)} />
        <KindPicks value={exKinds} onChange={setExKinds} />
        <select className="h-9 w-full rounded-md border bg-card px-2 text-sm" value={exBot} onChange={(e) => setExBot(e.target.value)}>
          <option value="">Choose a bot</option>
          {bots.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}
        </select>
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="outline" onClick={() => api("/panel/mirrors/admin", { channel: existing, bot_id: Number(exBot) }).then(() => toast("Bot is an admin")).catch((e) => toast.error(e.message))}>Add bot as admin for me</Button>
          <Button size="sm" onClick={() => api("/panel/mirrors/add", { channel: existing, bot_id: Number(exBot), kinds: exKinds }).then(() => { setExisting(""); toast("Mirror added"); return refresh() }).catch((e) => toast.error(e.message))}>Add existing channel</Button>
        </div>
      </CardBody></Card>
    </Page>
  )
}

function KindPicks({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  return <div className="flex flex-wrap gap-3 text-sm">{kinds.map(([id, label]) => <label key={id} className="flex items-center gap-1"><input type="checkbox" checked={value.includes(id)} onChange={(e) => onChange(e.target.checked ? [...value, id] : value.filter((k) => k !== id))} />{label}</label>)}</div>
}

function MirrorRow({ m, bots, onDone }: { m: NonNullable<PanelState["mirrors"]>[number]; bots: PanelState["bots"]; onDone: () => Promise<void> }) {
  const [bot, setBot] = useState(String(m.bot_id))
  const [k, setK] = useState(m.kinds)
  return (
    <div className="space-y-2 border-b pb-3 last:border-0">
      <div className="flex items-center justify-between gap-2">
        <div><div className="font-medium">{m.title || "Channel " + m.channel_id}</div><div className="text-xs text-muted-foreground">{m.username ? "t.me/" + m.username : "Private"} · {m.sent} sent{m.error ? " · " + m.error : ""}</div></div>
        <div className="flex gap-1">
          <Button size="sm" variant="outline" onClick={() => api("/panel/mirrors/visibility", { id: m.id, public: !m.username }).then(() => { toast(m.username ? "Private" : "Public"); return onDone() }).catch((e) => toast.error(e.message))}>{m.username ? "Make private" : "Make public"}</Button>
          <Button size="sm" variant="destructive" onClick={() => api("/panel/mirrors/remove", { id: m.id }).then(onDone).catch((e) => toast.error(e.message))}>Remove</Button>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <select className="h-8 rounded-md border bg-card px-2 text-xs" value={bot} onChange={(e) => setBot(e.target.value)}>{bots.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}</select>
        <KindPicks value={k} onChange={setK} />
        <Button size="sm" variant="outline" onClick={() => api("/panel/mirrors/save", { id: m.id, bot_id: Number(bot), kinds: k }).then(() => toast("Saved")).catch((e) => toast.error(e.message))}>Save</Button>
      </div>
    </div>
  )
}

export function Shared() {
  const { data: s } = usePanel()
  const qc = useQueryClient()
  const f = (s?.forum ?? null) as { group?: boolean; delete_originals?: boolean; fingerprint?: string; error?: string; trusted?: { fingerprint: string; own?: boolean; manual?: boolean; verified?: number; wrong?: number }[] } | null
  const [name, setName] = useState("")
  if (!f) return <Page title="Shared library" text="The shared library is not available."><p /></Page>
  async function act(action: string, body: unknown) {
    await api("/panel/forum/" + action, body)
    await qc.invalidateQueries({ queryKey: ["panel-state"] })
  }
  return (
    <Page title="Shared library" text="Several installations can use this one channel. A new file is posted again with a signed line.">
      {f.error && <p className="text-sm text-destructive">{f.error}</p>}
      <Card><CardBody className="space-y-3">
        <label className="flex items-center justify-between">Shared library <SwitchBox label="Shared library" checked={!!f.group} onCheckedChange={(on) => void act("group", { on }).catch((e) => toast.error(e.message))} /></label>
        <label className="flex items-center justify-between">Delete originals after a signed copy <SwitchBox label="Delete originals" checked={!!f.delete_originals} onCheckedChange={(on) => void act("originals", { on }).catch((e) => toast.error(e.message))} /></label>
        {f.fingerprint && <CopyField label="This installation" value={f.fingerprint} />}
        {(f.trusted ?? []).filter((r) => !r.own).map((r) => (
          <div key={r.fingerprint} className="flex items-center justify-between gap-2 text-sm">
            <span>{r.fingerprint} · {(r.verified ?? 0) + " posts checked"}{r.manual ? " · trusted by hand" : ""}</span>
            {r.manual && <Button size="sm" variant="destructive" onClick={() => void act("trust_remove", { fingerprint: r.fingerprint })}>Remove</Button>}
          </div>
        ))}
        <div className="flex gap-2"><Input value={name} placeholder="@librarybot" onChange={(e) => setName(e.target.value)} /><Button size="sm" onClick={() => act("trust_add", { username: name }).then(() => toast("Trusted")).catch((e) => toast.error(e.message))}>Trust this key</Button></div>
      </CardBody></Card>
    </Page>
  )
}

export function HomeVideos() {
  const q = useQuery({ queryKey: ["home"], queryFn: () => api<HomeView>("/panel/home"), refetchInterval: () => (pollingPaused() ? false : 8000) })
  const [channel, setChannel] = useState("")
  const [folder, setFolder] = useState("")
  const h = q.data
  useEffect(() => { if (h?.channel) setChannel("-100" + h.channel) }, [h?.channel])
  if (!h) return <Page title="Home videos" text="Personal videos and photos.">{q.isError ? <p className="text-sm text-destructive">{(q.error as Error).message}</p> : null}</Page>
  return (
    <Page title="Home videos" text="Personal videos and photos. Their own Telegram channel, and a Jellyfin library called Home Videos and Photos. Saving the channel needs a restart.">
      {h.problem && <p className="text-sm text-destructive">{h.problem}</p>}
      <Card><CardBody className="space-y-2">
        <FieldLabel>Channel link or id</FieldLabel>
        <Input value={channel} onChange={(e) => setChannel(e.target.value)} placeholder="https://t.me/c/1234567890/5 or -100…" />
        <div className="flex justify-end"><Button size="sm" onClick={() => run("/panel/home/channel", { channel }, "Saved").catch((e) => toast.error(e.message))}>Save channel</Button></div>
      </CardBody></Card>
      <Card><CardBody className="space-y-2">
        {(h.folders ?? []).map((f) => <div key={f.path} className="flex items-center justify-between text-sm"><code>{f.path}</code><Button size="sm" variant="destructive" onClick={() => api("/panel/home/folder", { path: f.path, remove: true }).then(() => q.refetch())}>Remove</Button></div>)}
        <div className="flex gap-2"><Input value={folder} placeholder="D:\Home\Videos" onChange={(e) => setFolder(e.target.value)} /><Button size="sm" onClick={() => api("/panel/home/folder", { path: folder }).then(() => { setFolder(""); return q.refetch() })}>Add folder</Button></div>
        <div className="flex justify-end"><Button size="sm" onClick={() => api("/panel/home/sync", {}).then(() => toast("Syncing")).catch((e) => toast.error(e.message))}>Sync into the channel</Button></div>
        {(h.files ?? []).map((f) => <div key={f.id} className="text-sm">{f.name} · {f.status}</div>)}
      </CardBody></Card>
    </Page>
  )
}

type HomeView = { channel?: number; problem?: string; folders?: { path: string }[]; files?: { id: number; name: string; status: string }[] }

export function Subtitles() {
  const { data: s } = usePanel()
  const qc = useQueryClient()
  const draft = useDirty((s?.language || "").split(",").filter(Boolean))
  if (!s) return null
  return (
    <Page title="Subtitles" text="Pick every language to keep. The highest one in the list is preferred. Leave them all off to skip subtitles.">
      <Card><CardBody className="space-y-3">
        <MultiSelect value={draft.value} onChange={draft.set} />
        <div className="flex justify-end"><Button size="sm" onClick={() => run("/panel/settings", { language: draft.value.join(",") }, "Saved").then(() => { draft.saved(); void qc.invalidateQueries({ queryKey: ["panel-state"] }) }).catch((e) => toast.error(e.message))}>Save language</Button></div>
      </CardBody></Card>
    </Page>
  )
}

export function Telegram() {
  const { data: s } = usePanel()
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const list = s?.telegram?.sessions ?? []
  return (
    <Page title="Telegram account" text="Sign in first. Torrents, Channels and the other tools open once an account is signed in. The account must be an admin of the library channel with permission to post.">
      <Card><CardBody className="space-y-3">
        {list.map((ses) => <SessionRow key={ses.user_id} ses={ses} onDone={() => qc.invalidateQueries({ queryKey: ["panel-state"] })} />)}
        {!list.length && <p className="text-sm text-muted-foreground">No Telegram account is signed in.</p>}
        <div className="flex justify-end"><Button size="sm" onClick={() => setOpen(true)}>Add account</Button></div>
        <p className="text-sm text-muted-foreground">A file larger than the account can send is refused, not split. Sign-in works from this computer or over Tailscale only. The session is stored in data/users and is not encrypted, so keep that folder like a password.</p>
      </CardBody></Card>
      <TelegramLoginDialog open={open} onOpenChange={setOpen} onDone={() => void qc.invalidateQueries({ queryKey: ["panel-state"] })} />
    </Page>
  )
}

function SessionRow({ ses, onDone }: { ses: Session; onDone: () => void }) {
  const name = ses.username ? "@" + ses.username : (ses.phone || "user " + ses.user_id)
  return (
    <div className="flex items-center justify-between gap-2">
      <div>
        <div className="font-medium">{name}</div>
        <div className="flex flex-wrap gap-1 pt-1">
          <StatusBadge ok={ses.connected}>{ses.connected ? "connected" : "not connected"}</StatusBadge>
          <StatusBadge ok={ses.admin}>{ses.admin ? "admin" : "not an admin"}</StatusBadge>
          <StatusBadge>{ses.premium ? "Premium" : "Standard"}</StatusBadge>
          {ses.error && <StatusBadge>{ses.error}</StatusBadge>}
        </div>
        {ses.busy && ses.total ? <div className="text-xs text-muted-foreground">Uploading {bytes(ses.progress || 0)} of {bytes(ses.total)}</div> : null}
      </div>
      <Button size="sm" variant="destructive" onClick={() => api("/panel/downloads/logout", { user_id: ses.user_id }).then(() => { toast("Signed out"); onDone() }).catch((e) => toast.error(e.message))}>Sign out</Button>
    </div>
  )
}

export function Bots() {
  const { data: s } = usePanel()
  const job = useJob()
  const qc = useQueryClient()
  const [token, setToken] = useState("")
  const [count, setCount] = useState(1)
  const [picked, setPicked] = useState<string[]>([])
  if (!s) return null
  return (
    <Page title="Bots" text="A bot is an account that reads the videos. The token is its password. This program never shows the full token again.">
      <Card><CardBody className="space-y-2">
        {s.bots.map((b) => (
          <div key={b.id} className="flex items-center justify-between gap-2 border-b pb-2 last:border-0">
            <div><div className="font-medium">{b.name}</div><div className="text-xs text-muted-foreground">…{b.hint}</div><div className="flex gap-1 pt-1"><StatusBadge ok={b.connected}>{b.connected ? "connected" : "saved"}</StatusBadge>{b.connected && <StatusBadge ok={b.admin}>{b.admin ? "admin" : "not an admin"}</StatusBadge>}{b.error && <StatusBadge>{b.error}</StatusBadge>}</div></div>
            <Button size="sm" variant="destructive" onClick={() => api("/panel/bots", { remove: b.id }).then(() => qc.invalidateQueries({ queryKey: ["panel-state"] })).catch((e) => toast.error(e.message))}>Remove</Button>
          </div>
        ))}
      </CardBody></Card>
      <Card><CardBody className="space-y-3">
        <h2 className="font-medium">Create bot automatically</h2>
        <p className="text-sm text-muted-foreground">Makes 1 to 3 bots with @BotFather and adds them to the main channel. A restart is needed before they connect.</p>
        <div className="flex items-center gap-2"><FieldLabel>Count</FieldLabel><Input className="w-20" type="number" min={1} max={3} value={count} onChange={(e) => setCount(Number(e.target.value))} /></div>
        <div className="flex justify-end"><Button size="sm" onClick={() => api("/panel/bots/create", { count }).then(() => toast("Creating")).catch((e) => toast.error(e.message))}>Create</Button></div>
        <ProgressSteps lines={job.data?.progress ?? []} />
        {job.data?.error && <p className="text-sm text-destructive">{job.data.error}</p>}
        {(job.data?.owned ?? []).map((b) => <label key={b.username} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={picked.includes(b.username)} onChange={(e) => setPicked(e.target.checked ? [...picked, b.username] : picked.filter((n) => n !== b.username))} />@{b.username}{b.name ? " · " + b.name : ""}</label>)}
        {!!job.data?.owned?.length && <div className="flex justify-end"><Button size="sm" onClick={() => api("/panel/bots/reuse", { usernames: picked }).then(() => toast("Reusing")).catch((e) => toast.error(e.message))}>Use the selected bots</Button></div>}
      </CardBody></Card>
      <Card><CardBody className="space-y-2">
        <h2 className="font-medium">Add a bot</h2>
        <Collapse title="How to create a bot">
          <ol className="list-decimal space-y-1 pl-4">
            <li>Open @BotFather in Telegram.</li>
            <li>Send /newbot.</li>
            <li>Choose a display name, then a username that ends in bot.</li>
            <li>Paste the token below. Do not post it in the channel.</li>
          </ol>
        </Collapse>
        <FieldLabel>Paste token</FieldLabel>
        <Input value={token} placeholder="123456789:AAE..." onChange={(e) => setToken(e.target.value)} />
        <div className="flex justify-end"><Button size="sm" variant="outline" onClick={() => api("/panel/bots", { add: token.trim() }).then(() => { setToken(""); toast("Saved"); return qc.invalidateQueries({ queryKey: ["panel-state"] }) }).catch((e) => toast.error(e.message))}>Paste token</Button></div>
      </CardBody></Card>
    </Page>
  )
}

export function ApiPage() {
  const { data: s } = usePanel()
  const [id, setId] = useState("")
  const [hash, setHash] = useState("")
  const [show, setShow] = useState(!s?.default_api)
  useEffect(() => { if (s) setShow(!s.default_api) }, [s?.default_api])
  if (!s) return null
  return (
    <Page title="Telegram API" text="The API ID and API hash identify this program to Telegram. They are not the bot token.">
      <Card><CardBody className="space-y-3">
        {s.default_api && !show && <div className="flex items-center justify-between"><div><div className="font-medium">Default</div><div className="text-sm text-muted-foreground">You're using the default. Replace it here if you want your own.</div></div><Button size="sm" variant="outline" onClick={() => setShow(true)}>Replace</Button></div>}
        {show && <>
          <Collapse title="Where do I get these?">
            <p>Telegram does not offer a public ID you can download or copy from someone else. Each person creates a pair, free, with their own phone number. This program speaks Telegram’s own protocol, and that protocol refuses a connection without this pair.</p>
            <p className="pt-2">Open my.telegram.org, then API development tools, and copy the numeric App api_id and the App api_hash.</p>
          </Collapse>
          <FieldLabel>API ID</FieldLabel>
          <Input value={id} placeholder={s.api_id ? String(s.api_id) : "api_id"} onChange={(e) => setId(e.target.value)} />
          <FieldLabel>API hash</FieldLabel>
          <Input value={hash} placeholder="Leave empty to keep the saved hash" onChange={(e) => setHash(e.target.value)} />
          <div className="flex justify-end"><Button size="sm" onClick={() => run("/panel/settings", { api_id: id, api_hash: hash }, "Saved").catch((e) => toast.error(e.message))}>Save</Button></div>
        </>}
      </CardBody></Card>
    </Page>
  )
}

const lookbacks: [string, string][] = [["1", "1 day"], ["3", "3 days"], ["7", "7 days"], ["14", "14 days"], ["30", "30 days"], ["90", "90 days"], ["365", "1 year"], ["0", "All history"]]

export function Sources() { return <RulesPage kind="direct" /> }
export function News() { return <RulesPage kind="news" /> }

function RulesPage({ kind }: { kind: "direct" | "news" }) {
  const panel = usePanel()
  const q = useQuery({
    queryKey: ["sources"],
    queryFn: () => api<SourcesView>("/panel/sources"),
    refetchInterval: () => (pollingPaused() ? false : 5000),
  })
  const signed = (panel.data?.telegram?.sessions?.length ?? 0) > 0
  const v = q.data
  const settings = kind === "news" ? v?.news?.settings : v?.settings
  const draft = useDirty(settings)
  if (!signed) return <Page title={kind === "news" ? "News channels" : "Direct files"} text=""><div className="rounded-md border px-3 py-2 text-sm">No Telegram account is signed in. <Link className="underline" to="/telegram">Accounts</Link></div></Page>
  if (!v || !draft.value) return null
  const news = kind === "news"
  const list = news ? v.news?.channels ?? [] : v.sources ?? []
  const recent = news ? v.news?.recent ?? [] : v.recent ?? []
  return (
    <Page title={news ? "News channels" : "Direct files"} text={news ? "These channels post a bot link instead of the film. Any free signed-in account opens the link." : "Give the signed-in Telegram account other channels that post the film itself."}>
      <Card><CardBody className="flex items-center justify-between">
        <div className="font-medium">{draft.value.enabled ? "On" : "Off"}</div>
        <SwitchBox label="Enabled" checked={!!draft.value.enabled} onCheckedChange={(enabled) => api(news ? "/panel/sources/news" : "/panel/sources/settings", { enabled }).then(() => q.refetch()).catch((e) => toast.error(e.message))} />
      </CardBody></Card>
      <ChannelAdd news={news} onDone={() => q.refetch()} />
      <Card><CardBody className="space-y-2">
        {list.map((row) => (
          <div key={row.id} className="flex items-center justify-between gap-2 text-sm">
            <div><div className="font-medium">{row.title || row.username || row.channel_id}</div><div className="text-xs text-muted-foreground">{row.copied ?? 0} copied{row.error ? " · " + row.error : ""}</div></div>
            <div className="flex gap-1">
              <Button size="sm" variant="outline" onClick={() => api(news ? "/panel/sources/news_pause" : "/panel/sources/pause", { id: row.id, enabled: !row.enabled }).then(() => q.refetch())}>{row.enabled ? "Pause" : "Resume"}</Button>
              {!news && <Button size="sm" variant="outline" onClick={() => api("/panel/sources/strict", { id: row.id, strict: !row.strict }).then(() => q.refetch())}>{row.strict ? "Any file" : "Strict"}</Button>}
              <Button size="sm" variant="destructive" onClick={() => api(news ? "/panel/sources/news_remove" : "/panel/sources/remove", { id: row.id }).then(() => q.refetch())}>Remove</Button>
            </div>
          </div>
        ))}
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="outline" onClick={() => api("/panel/sources/check", {}).then(() => toast("Checking"))}>Check now</Button>
          <Button size="sm" variant="outline" onClick={() => api("/panel/sources/rescan", {}).then(() => toast("Looking again"))}>Look again</Button>
        </div>
      </CardBody></Card>
      <Card><CardBody className="space-y-2">
        <h2 className="font-medium">What to copy</h2>
        <Collapse title="How do I get a channel id?">
          <p>A public channel is @name. For a private one, forward a message to @id_bot or @getidsbot. An invite link does not work.</p>
        </Collapse>
        <select className="h-9 rounded-md border bg-card px-2 text-sm" value={String(draft.value.look_back_days ?? 0)} onChange={(e) => draft.set({ ...draft.value!, look_back_days: Number(e.target.value) })}>{lookbacks.map(([id, label]) => <option key={id} value={id}>{label}</option>)}</select>
        <div className="grid gap-2 sm:grid-cols-2">
          <Num label="Check every, minutes" k="every_minutes" draft={draft} />
          <Num label="Shortest video, minutes" k="min_minutes" draft={draft} />
          <Num label="Smallest file, MB" k="min_mb" draft={draft} />
          <Num label="Largest file, GB" k="max_gb" draft={draft} />
          <Num label="Most videos per day" k="max_per_day" draft={draft} />
          <Num label="Pause, seconds" k="pause_seconds" draft={draft} />
        </div>
        <FieldLabel>Skip words</FieldLabel>
        <Input value={draft.value.skip_words || ""} onChange={(e) => draft.set({ ...draft.value!, skip_words: e.target.value })} />
        <FieldLabel>Only words</FieldLabel>
        <Input value={draft.value.only_words || ""} onChange={(e) => draft.set({ ...draft.value!, only_words: e.target.value })} />
        <label className="flex items-center justify-between text-sm">Only files that look like a film or an episode <SwitchBox label="Need a title" checked={!!draft.value.need_title} onCheckedChange={(need_title) => draft.set({ ...draft.value!, need_title })} /></label>
        <div className="flex justify-end"><Button size="sm" onClick={() => api(news ? "/panel/sources/news_settings" : "/panel/sources/settings", draft.value).then(() => { draft.saved(); toast("Saved"); return q.refetch() }).catch((e) => toast.error(e.message))}>Save rules</Button></div>
      </CardBody></Card>
      <Card><CardBody className="space-y-1 text-sm">
        <h2 className="font-medium">Recent</h2>
        {recent.map((r, i) => <div key={i}>{("name" in r ? r.name : r.url) || r.state}{r.error || r.detail ? " · " + (r.error || r.detail) : ""}</div>)}
        {!recent.length && <p className="text-muted-foreground">Nothing yet.</p>}
      </CardBody></Card>
    </Page>
  )
}

type Rule = { enabled?: boolean; look_back_days?: number; every_minutes?: number; min_minutes?: number; min_mb?: number; max_gb?: number; max_per_day?: number; pause_seconds?: number; skip_words?: string; only_words?: string; need_title?: boolean }
type Src = { id: number; title?: string; username?: string; channel_id: number; enabled: boolean; strict?: boolean; copied?: number; error?: string }
type Recent = { name?: string; url?: string; state?: string; error?: string; detail?: string }
type SourcesView = { settings?: Rule; sources?: Src[]; recent?: Recent[]; news?: { settings?: Rule; channels?: Src[]; recent?: Recent[] } }

function Num({ label, k, draft }: { label: string; k: keyof Rule; draft: { value: Rule | undefined; set: (v: Rule) => void } }) {
  return <label className="text-sm">{label}<Input type="number" value={Number(draft.value?.[k] ?? 0)} onChange={(e) => draft.set({ ...draft.value, [k]: Number(e.target.value) })} /></label>
}

function ChannelAdd({ news, onDone }: { news: boolean; onDone: () => void }) {
  const [value, setValue] = useState("")
  return <Card><CardBody className="flex gap-2"><Input value={value} placeholder="@channel or -100…" onChange={(e) => setValue(e.target.value)} /><Button size="sm" onClick={() => api(news ? "/panel/sources/news_add" : "/panel/sources/add", { channel: value }).then(() => { setValue(""); onDone() }).catch((e) => toast.error(e.message))}>Add channel</Button></CardBody></Card>
}

export function Local() {
  const q = useQuery({ queryKey: ["local"], queryFn: () => api<LocalView>("/panel/local"), refetchInterval: () => (pollingPaused() ? false : 4000) })
  const [path, setPath] = useState("")
  const [filter, setFilter] = useState("all")
  const v = q.data
  if (!v) return <Page title="Local files" text="Videos already on this computer.">{null}</Page>
  const files = (v.categories ?? []).flatMap((c) => (c.groups ?? []).flatMap((g) => g.files ?? []))
  const shown = files.filter((f) => filter === "all" || bucket(f.status) === filter)
  return (
    <Page title="Local files" text="Videos already on this computer, sorted into films and series. Nothing is uploaded or deleted until you press a button.">
      <div className="grid gap-2 sm:grid-cols-3">
        <Stat label="Only on this computer" value={String(v.totals?.local?.count ?? 0)} sub={bytes(v.totals?.local?.bytes ?? 0)} />
        <Stat label="On the way" value={String(v.totals?.busy?.count ?? 0)} sub={bytes(v.totals?.busy?.bytes ?? 0)} />
        <Stat label="In Telegram" value={String(v.totals?.synced?.count ?? 0)} sub={bytes(v.totals?.synced?.bytes ?? 0)} />
      </div>
      {v.problem && <p className="text-sm">{v.problem} <Link className="underline" to="/telegram">Add account</Link></p>}
      <Card><CardBody className="space-y-2">
        {(v.folders ?? []).map((f) => <div key={f.path} className="flex justify-between text-sm"><code>{f.path}</code><Button size="sm" variant="destructive" onClick={() => api("/panel/local/folder", { remove: f.path }).then(() => q.refetch())}>Remove</Button></div>)}
        <div className="flex gap-2"><Input value={path} placeholder="D:\Library\Movie" onChange={(e) => setPath(e.target.value)} /><Button size="sm" onClick={() => api("/panel/local/folder", { add: path }).then(() => { setPath(""); return q.refetch() }).catch((e) => toast.error(e.message))}>Add folder</Button><Button size="sm" variant="outline" onClick={() => api("/panel/local/scan", {}).then(() => q.refetch())}>Scan now</Button></div>
      </CardBody></Card>
      <Card><CardBody className="space-y-2">
        <div className="flex flex-wrap justify-between gap-2">
          <Toggles value={filter} onChange={setFilter} options={[["all", "All"], ["local", "Only here"], ["busy", "On the way"], ["synced", "In Telegram"], ["problem", "Needs a look"]]} />
          <div className="flex gap-2">
            <Button size="sm" onClick={() => api("/panel/local/upload", { ids: files.filter((f) => f.status === "local").map((f) => f.id) }).then(() => q.refetch()).catch((e) => toast.error(e.message))}>Upload everything that is only here</Button>
            <Button size="sm" variant="destructive" onClick={() => confirmDialog("Delete from this computer?", "This deletes every local copy that is already in Telegram.", "Delete").then((ok) => { if (ok) return api("/panel/local/delete_synced", {}) }).then(() => q.refetch())}>Delete every local copy that is in Telegram</Button>
          </div>
        </div>
        {(v.categories ?? []).map((c) => (
          <Collapse key={c.id} title={c.label}>
            {(c.groups ?? []).map((g) => <div key={g.title} className="py-1">{g.title}{g.year ? " (" + g.year + ")" : ""} · {(g.files ?? []).filter((f) => shown.includes(f)).length} files</div>)}
          </Collapse>
        ))}
      </CardBody></Card>
    </Page>
  )
}

function bucket(status: string) {
  if (status === "waiting" || status === "uploading" || status === "checking") return "busy"
  return status
}

type LocalFile = { id: number; status: string; name?: string; size?: number }
type LocalView = { problem?: string; folders?: { path: string }[]; totals?: Record<string, { count?: number; bytes?: number }>; categories?: { id: string; label: string; groups?: { title: string; year?: number; files?: LocalFile[] }[] }[] }

export function Downloads() {
  const { data: s } = usePanel()
  const d = s?.downloads as { enabled?: boolean; dir?: string; free_bytes?: number; error?: string; warning?: string; pace?: Record<string, number | string>; containers?: { name: string; running: boolean }[]; queue?: { name?: string; state?: string; error?: string }[] } | null
  const pace = useDirty({
    mb_per_sec: num(d?.pace, "mb_per_sec", "MBPerSec"),
    rest_minutes: num(d?.pace, "rest_minutes", "RestMinutes"),
    long_every_gb: num(d?.pace, "long_every_gb", "LongEveryGB"),
    long_rest_minutes: num(d?.pace, "long_rest_minutes", "LongRestMinutes"),
    daily_gb: num(d?.pace, "daily_gb", "DailyGB"),
    hours: String(d?.pace?.hours ?? d?.pace?.Hours ?? ""),
  })
  const qc = useQueryClient()
  if (!d) return null
  return (
    <Page title="Torrents" text="One switch for the whole chain. It is off until you turn it on.">
      <Card><CardBody className="space-y-2">
        <div className="flex items-center justify-between"><div><div className="font-medium">{d.enabled ? "Torrent downloads are on" : "Torrent downloads are off"}</div><div className="text-xs text-muted-foreground">{[d.dir, d.free_bytes ? (d.free_bytes / 1024 / 1024 / 1024).toFixed(1) + " GiB free" : ""].filter(Boolean).join(" · ")}</div></div>
          <SwitchBox label="Torrent downloads" checked={!!d.enabled} onCheckedChange={(enabled) => api("/panel/automation", { enabled }).then(() => qc.invalidateQueries({ queryKey: ["panel-state"] })).catch((e) => toast.error(e.message))} /></div>
        {d.enabled && <p className="text-sm text-destructive">Torrents connect this computer to strangers and can be illegal where you live. Your address is visible to other people in the swarm. Leave this off unless you accept that.</p>}
      </CardBody></Card>
      <Card><CardBody className="space-y-2">
        <h2 className="font-medium">Pace</h2>
        <div className="grid gap-2 sm:grid-cols-3">
          <label className="text-sm">Megabytes per second<Input value={pace.value.mb_per_sec} onChange={(e) => pace.set({ ...pace.value, mb_per_sec: Number(e.target.value) })} /></label>
          <label className="text-sm">Minutes between files<Input value={pace.value.rest_minutes} onChange={(e) => pace.set({ ...pace.value, rest_minutes: Number(e.target.value) })} /></label>
          <label className="text-sm">Long rest after this many GB<Input value={pace.value.long_every_gb} onChange={(e) => pace.set({ ...pace.value, long_every_gb: Number(e.target.value) })} /></label>
          <label className="text-sm">Long rest, minutes<Input value={pace.value.long_rest_minutes} onChange={(e) => pace.set({ ...pace.value, long_rest_minutes: Number(e.target.value) })} /></label>
          <label className="text-sm">Daily GB per account<Input value={pace.value.daily_gb} onChange={(e) => pace.set({ ...pace.value, daily_gb: Number(e.target.value) })} /></label>
          <label className="text-sm">Hours<Input value={pace.value.hours} placeholder="01-08" onChange={(e) => pace.set({ ...pace.value, hours: e.target.value })} /></label>
        </div>
        <div className="flex justify-end"><Button size="sm" onClick={() => api("/panel/downloads/pace", pace.value).then(() => { pace.saved(); toast("Pace saved") }).catch((e) => toast.error(e.message))}>Save pace</Button></div>
      </CardBody></Card>
      <Card><CardBody className="space-y-1 text-sm"><h2 className="font-medium">Stack</h2>{d.error && <p className="text-destructive">{d.error}</p>}{d.warning && <p>{d.warning}</p>}{(d.containers ?? []).map((c) => <div key={c.name} className="flex justify-between">{c.name}<StatusBadge ok={c.running}>{c.running ? "running" : "stopped"}</StatusBadge></div>)}</CardBody></Card>
      <Card><CardBody className="space-y-1 text-sm"><h2 className="font-medium">Queue</h2>{(d.queue ?? []).map((item, i) => <div key={i} className="flex justify-between">{item.name}<StatusBadge>{item.state || "queued"}</StatusBadge></div>)}{!d.queue?.length && <p className="text-muted-foreground">Nothing queued.</p>}</CardBody></Card>
    </Page>
  )
}

function num(pace: Record<string, number | string> | undefined, a: string, b: string) {
  const v = pace?.[a] ?? pace?.[b]
  return Number(v ?? 0)
}

export function Away() {
  const { data: s } = usePanel()
  return (
    <Page title="Away from home" text="At home, open an address tagged At home. Away from home, use Tailscale. It is a private network between your own devices. Nothing is published on the public internet.">
      <Card><CardBody>
        <ol className="list-decimal space-y-1 pl-4 text-sm">
          <li>Install Tailscale on this computer.</li>
          <li>Install it on the phone or the TV.</li>
          <li>Sign both devices in to the same Tailscale account.</li>
          <li>Open the address tagged Away from home below.</li>
          <li>Do not turn on Tailscale Funnel. Funnel would publish Jellyfin to the whole internet, and it is a poor fit for video. No router port has to be opened.</li>
        </ol>
      </CardBody></Card>
      <Card><CardBody className="space-y-2">{(s?.addresses ?? []).map((a) => <div key={a.url} className="flex items-center justify-between gap-2"><div><StatusBadge ok={!a.away}>{a.away ? "Away from home" : "At home"}</StatusBadge> <code className="text-sm">{a.url}</code></div><CopyField label="" value={a.url} /></div>)}</CardBody></Card>
    </Page>
  )
}
