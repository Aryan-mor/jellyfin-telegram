import { useEffect, useState, type ReactNode } from "react"
import { Link } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Eye, EyeOff, RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { api, panelCode, pollingPaused } from "@/lib/api"
import { ago, bytes, cn, plural } from "@/lib/utils"
import {
  Alert, Button, Card, CardBody, Checkbox, Collapse, Disclosure, Empty, FieldLabel, Input, Progress, Select,
  SwitchBox, Table, TBody, TD, TH, THead, Toggles, TR,
} from "@/components/ui"
import { confirmDialog, CopyField, MultiSelect, ProgressSteps, StatusBadge, TelegramLoginDialog, type Tone } from "@/components/widgets"
import { useJob, usePanel, type PanelState, type Session } from "@/panel/shell"

// ---------- shared pieces ----------

function Page({ title, text, children }: { title: string; text: string; children: ReactNode }) {
  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {text && <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{text}</p>}
      </div>
      {children}
    </div>
  )
}

// Section is one card with a heading, an optional line under it, and buttons on the right.
function Section({ title, text, actions, children, className }: { title?: string; text?: ReactNode; actions?: ReactNode; children?: ReactNode; className?: string }) {
  return (
    <Card>
      <CardBody className={cn("space-y-3", className)}>
        {(title || actions) && (
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0">
              {title && <h2 className="font-medium">{title}</h2>}
              {text && <p className="text-sm text-muted-foreground">{text}</p>}
            </div>
            {actions && <div className="flex shrink-0 flex-wrap gap-2">{actions}</div>}
          </div>
        )}
        {children}
      </CardBody>
    </Card>
  )
}

// Row is a label on the left and a control on the right.
function Row({ title, text, children }: { title: ReactNode; text?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <div className="text-sm font-medium">{title}</div>
        {text && <div className="text-sm text-muted-foreground">{text}</div>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

function useDirty<T>(remote: T) {
  const [value, setValue] = useState(remote)
  const [dirty, setDirty] = useState(false)
  const key = JSON.stringify(remote)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { if (!dirty) setValue(remote) }, [key, dirty])
  return { value, dirty, set: (v: T) => { setDirty(true); setValue(v) }, saved: () => setDirty(false) }
}

function fail(e: unknown) {
  toast.error(e instanceof Error ? e.message : "Something went wrong")
}

function useRefreshState() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: ["panel-state"] })
}

// Busy runs one action and disables its button meanwhile.
function useBusy() {
  const [busy, setBusy] = useState<string | null>(null)
  async function run<T>(key: string, fn: () => Promise<T>): Promise<T | undefined> {
    setBusy(key)
    try { return await fn() } catch (e) { fail(e); return undefined } finally { setBusy(null) }
  }
  return { busy, run }
}

function Stat({ label, value, sub, to }: { label: string; value: string; sub: string; to?: string }) {
  const body = <><div className="text-xs text-muted-foreground">{label}</div><div className="truncate text-lg font-semibold">{value}</div><div className="truncate text-xs text-muted-foreground">{sub}</div></>
  if (to) return <Link to={to} className="rounded-lg border bg-card p-3 transition-colors hover:bg-accent/50">{body}</Link>
  return <div className="rounded-lg border bg-card p-3">{body}</div>
}

function SignInFirst() {
  return <Alert tone="warn" action={<Button size="sm" variant="outline" asChild><Link to="/telegram">Accounts</Link></Button>}>No Telegram account is signed in. Sign one in first.</Alert>
}

// ---------- Overview ----------

type Forum = { group?: boolean; delete_originals?: boolean; fingerprint?: string; error?: string; trusted?: { fingerprint: string; own?: boolean; manual?: boolean; verified?: number; wrong?: number }[] }

export function Overview() {
  const { data: s } = usePanel()
  if (!s) return null
  const signed = (s.telegram?.sessions?.length ?? 0) > 0
  const botsOk = s.bots.filter((b) => b.ok && b.admin).length
  const phone = s.addresses.find((a) => !a.away && !/localhost|127\.0\.0\.1/.test(a.url))
  const share = phone ? phone.url.replace(/\/$/, "") + "/panel?code=" + panelCode() : ""
  const pass = s.accounts.some((a) => a.app === "Jellyfin" || a.app === "Seerr")
  const forum = s.forum as Forum | null
  const dl = s.downloads as { enabled?: boolean; queue?: unknown[] } | null
  const mirrors = s.mirrors?.length ?? 0
  let forumValue = "Off", forumSub = "Plain channel"
  if (forum?.error) { forumValue = "Problem"; forumSub = "Open Shared library" }
  else if (forum?.fingerprint) { forumValue = "On"; forumSub = "Key " + forum.fingerprint }
  else if (forum?.group) { forumValue = "Waiting"; forumSub = "Restart to start it" }
  return (
    <Page title="Overview" text="Everything this server runs, and where to open it.">
      {pass && (
        <Alert action={<Button size="sm" variant="outline" asChild><Link to="/accounts">Sign in details</Link></Button>}>
          Don't know your Jellyfin or Seerr password? The user and password for each app are on the Sign in details page.
        </Alert>
      )}
      {!signed && <SignInFirst />}
      <div className="grid gap-2 sm:grid-cols-3">
        <AppTile href="/" name="Jellyfin" text="Watch your library" />
        {s.seerr && <AppTile href="/seerr/" name="Seerr" text="Request films and series" />}
        {s.fladder && <AppTile href="/fladder/" name="Fladder" text="A nicer web client" />}
      </div>
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Bots" value={botsOk + " / " + s.bots.length} sub={botsOk === s.bots.length ? "All ready" : "Some need a look"} to="/bots" />
        <Stat label="Channel" value={"-100" + s.channel_id} sub={(s.language ? "Subtitles " + s.language : "No subtitles") + (mirrors ? " · " + plural(mirrors, "mirror") : "")} to="/channel" />
        <Stat label="Shared library" value={forumValue} sub={forumSub} to="/channel/shared" />
        {s.downloads && <Stat label="Downloads" value={dl?.enabled ? "On" : "Off"} sub={dl?.enabled ? (dl.queue?.length ?? 0) + " in the queue" : "Files in the channel still play"} to={signed ? "/downloads" : undefined} />}
      </div>
      <Section title="Open from another device" text="On this computer, open /panel. From a phone on the same Wi-Fi, open this link. The part after code= is the secret that keeps the panel private.">
        <CopyField label="Share link" value={share || "No network address yet"} />
        {s.fladder && <a className="text-sm underline underline-offset-2" href="https://github.com/DonutWare/Fladder/releases" target="_blank" rel="noreferrer">Download other Fladder versions</a>}
      </Section>
    </Page>
  )
}

function AppTile({ href, name, text }: { href: string; name: string; text: string }) {
  return <a className="rounded-lg border bg-card p-3 transition-colors hover:bg-accent/50" href={href} target="_blank" rel="noreferrer"><div className="font-medium">{name}</div><div className="text-sm text-muted-foreground">{text}</div></a>
}

// ---------- Sign in details ----------

export function Accounts() {
  const { data: s } = usePanel()
  const list = (s?.accounts ?? []).slice()
  // Radarr and Sonarr belong to the downloads feature, so their accounts come from its status.
  const d = (s?.downloads ?? null) as { radarr_user?: string; radarr_password?: string; sonarr_user?: string; sonarr_password?: string } | null
  if (d?.radarr_user) list.push({ app: "Radarr", path: "/radarr/", user: d.radarr_user, password: d.radarr_password, note: "Looks for films. Only needed to check on a download." })
  if (d?.sonarr_user) list.push({ app: "Sonarr", path: "/sonarr/", user: d.sonarr_user, password: d.sonarr_password, note: "Looks for series. Only needed to check on a download." })
  return (
    <Page title="Sign in details" text="The account to use in each app. Share this computer's address with the people who should watch, and send them to Jellyfin first.">
      <Card>
        <Table>
          <THead><TR><TH className="pl-4">App</TH><TH>User</TH><TH>Password</TH></TR></THead>
          <TBody>
            {list.map((a) => (
              <TR key={a.app}>
                <TD className="pl-4">
                  <a className="font-medium underline-offset-2 hover:underline" href={a.path} target="_blank" rel="noreferrer">{a.app}</a>
                  {a.note && <div className="text-xs text-muted-foreground">{a.note}</div>}
                </TD>
                {a.user ? <>
                  <TD><CopyField label="" value={a.user} /></TD>
                  <TD><CopyField label="" value={a.password || ""} /></TD>
                </> : <TD className="text-muted-foreground"><StatusBadge>your own account</StatusBadge></TD>}
              </TR>
            ))}
          </TBody>
        </Table>
        {!list.length && <Empty>No accounts to show yet.</Empty>}
      </Card>
    </Page>
  )
}

// ---------- Main channel ----------

export function Channel() {
  const { data: s } = usePanel()
  const refresh = useRefreshState()
  const [editing, setEditing] = useState(false)
  const [channel, setChannel] = useState("")
  const { busy, run } = useBusy()
  if (!s) return null
  const search = s.telegram_search !== false
  return (
    <Page title="Main channel" text="The channel that holds the library. Replacing it is refused once files are indexed, because the library would point at the wrong messages.">
      <Section title="Library channel" actions={!editing && <Button size="sm" variant="outline" onClick={() => setEditing(true)}>Replace</Button>}>
        <code className="text-sm">-100{s.channel_id}</code>
        {editing && <div className="space-y-2">
          <FieldLabel htmlFor="main-channel">New channel</FieldLabel>
          <Input id="main-channel" value={channel} placeholder="https://t.me/c/1234567890/5 or -100…" onChange={(e) => setChannel(e.target.value)} />
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>Cancel</Button>
            <Button size="sm" disabled={!channel.trim() || busy === "save"} onClick={() => void run("save", async () => {
              await api("/panel/settings", { channel })
              toast("Saved. Restart to use the new channel.")
              setEditing(false); setChannel(""); await refresh()
            })}>Save</Button>
          </div>
        </div>}
      </Section>
      <Section>
        <Row title="Telegram search" text="Look up a requested title on Telegram and copy a file that is found.">
          <SwitchBox label="Telegram search" checked={search} onCheckedChange={(on) => void api("/panel/search", { on }).then(refresh).catch(fail)} />
        </Row>
      </Section>
    </Page>
  )
}

// ---------- Mirrors ----------

const kinds = [["movie", "Films"], ["series", "Series"], ["music", "Music"], ["video", "General video"]] as const
const usernameOk = (u: string) => /^[A-Za-z][A-Za-z0-9_]{3,30}[A-Za-z0-9]$/.test(u) && !u.includes("__")

function KindPicks({ value, onChange, disabled }: { value: string[]; onChange: (v: string[]) => void; disabled?: boolean }) {
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-2">
      {kinds.map(([id, label]) => (
        <Checkbox key={id} label={label} disabled={disabled} checked={value.includes(id)} onCheckedChange={(on) => onChange(on ? [...value, id] : value.filter((k) => k !== id))} />
      ))}
    </div>
  )
}

function botOptions(bots: PanelState["bots"], extra?: number): [string, string][] {
  const out: [string, string][] = bots.map((b) => [String(b.id), b.name + (b.connected ? "" : " (after restart)")])
  if (extra && !bots.some((b) => b.id === extra)) out.push([String(extra), "bot " + extra])
  return out
}

export function Mirrors() {
  const { data: s } = usePanel()
  const job = useJob()
  const refresh = useRefreshState()
  const [open, setOpen] = useState(false)
  const [mine, setMine] = useState(false)
  const [title, setTitle] = useState("")
  const [pub, setPub] = useState(true)
  const [user, setUser] = useState("")
  const [pick, setPick] = useState<string[]>(["movie", "series"])
  const [bot, setBot] = useState("")
  const [existing, setExisting] = useState("")
  const [exBot, setExBot] = useState("")
  const [exKinds, setExKinds] = useState<string[]>(["movie", "series"])
  const { busy, run } = useBusy()
  const running = !!job.data?.running
  useEffect(() => { if (mine && job.data && !job.data.running) void refresh() }, [mine, job.data?.running])
  if (!s) return null
  const live = s.bots.filter((b) => b.connected)
  const chosen = bot || (live[0] ? String(live[0].id) : "new")
  const badUser = pub && user.trim() !== "" && !usernameOk(user.trim().replace(/^@/, ""))
  const createOptions: [string, string][] = [...live.map((b) => [String(b.id), b.name] as [string, string]), ["new", "Create a new bot (needs a restart)"]]
  return (
    <Page title="Mirrors" text="Each mirror is another channel. Choose which parts are copied there, and which bot posts them.">
      <Section title="Create mirror" text="Makes a new channel with your Telegram account, adds a bot as admin, and starts copying. Nothing to do in Telegram."
        actions={!open && <Button size="sm" onClick={() => setOpen(true)}>Create mirror</Button>}>
        {open && <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5 sm:col-span-2">
            <FieldLabel htmlFor="m-title">Channel name</FieldLabel>
            <Input id="m-title" value={title} placeholder="Films" onChange={(e) => setTitle(e.target.value)} />
          </div>
          <div className="sm:col-span-2"><Row title="Public" text={pub ? "Anyone with the link can join. A random username is chosen unless you type one." : "Only people you invite can join."}><SwitchBox label="Public" checked={pub} onCheckedChange={setPub} /></Row></div>
          {pub && <div className="space-y-1.5 sm:col-span-2">
            <FieldLabel htmlFor="m-user">Username, optional</FieldLabel>
            <Input id="m-user" value={user} placeholder="Leave empty for a random one" onChange={(e) => setUser(e.target.value)} aria-invalid={badUser} />
            {badUser && <p className="text-xs text-destructive">Use 5 to 32 letters, digits or _, starting with a letter.</p>}
          </div>}
          <div className="space-y-1.5 sm:col-span-2"><FieldLabel>Parts to copy</FieldLabel><KindPicks value={pick} onChange={setPick} /></div>
          <div className="space-y-1.5 sm:col-span-2"><FieldLabel>Bot that posts</FieldLabel><Select label="Bot that posts" value={chosen} onValueChange={setBot} options={createOptions} /></div>
          <div className="flex justify-end gap-2 sm:col-span-2">
            <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>Cancel</Button>
            <Button size="sm" disabled={running || !title.trim() || !pick.length || badUser || busy === "create"} onClick={() => void run("create", async () => {
              await api("/panel/mirrors/create", { title: title.trim(), public: pub, username: user.trim().replace(/^@/, ""), kinds: pick, bot: chosen === "new" ? "new" : Number(chosen) })
              setMine(true); setOpen(false); setTitle(""); setUser("")
              await job.refetch()
            })}>{running ? "Working…" : "Create"}</Button>
          </div>
        </div>}
        {(mine || running) && <>
          <ProgressSteps lines={job.data?.progress ?? []} />
          {job.data?.error && <Alert tone="bad">{job.data.error}</Alert>}
          {job.data?.result?.link && <CopyField label="Public link" value={job.data.result.link} />}
        </>}
      </Section>

      <Card>
        <Table>
          <THead><TR><TH className="pl-4">Channel</TH><TH>Parts</TH><TH className="w-48">Bot</TH><TH className="pr-4 text-right">Actions</TH></TR></THead>
          <TBody>
            {(s.mirrors ?? []).map((m) => <MirrorRow key={m.id} m={m} bots={s.bots} onDone={refresh} />)}
          </TBody>
        </Table>
        {!s.mirrors?.length && <Empty>No mirror yet.</Empty>}
      </Card>

      <Section title="Add existing channel" text="A channel you already own. The bot is made an admin there with your account, so nothing has to be done by hand.">
        <Input value={existing} placeholder="https://t.me/c/1234567890/5 or @films" onChange={(e) => setExisting(e.target.value)} />
        <KindPicks value={exKinds} onChange={setExKinds} />
        <Select label="Bot that posts" value={exBot} onValueChange={setExBot} placeholder={live.length ? "Choose a bot" : "Add a bot first"} options={live.map((b) => [String(b.id), b.name])} />
        <div className="flex justify-end">
          <Button size="sm" disabled={!existing.trim() || !exBot || !exKinds.length || busy === "existing"} onClick={() => void run("existing", async () => {
            await api("/panel/mirrors/admin", { channel: existing.trim(), bot_id: Number(exBot) })
            await api("/panel/mirrors/add", { channel: existing.trim(), bot_id: Number(exBot), kinds: exKinds })
            setExisting(""); toast("Mirror added"); await refresh()
          })}>Add channel</Button>
        </div>
      </Section>
    </Page>
  )
}

function MirrorRow({ m, bots, onDone }: { m: NonNullable<PanelState["mirrors"]>[number]; bots: PanelState["bots"]; onDone: () => Promise<unknown> }) {
  const { busy, run } = useBusy()
  const botLive = bots.some((b) => b.id === m.bot_id && b.connected)
  // Every change is saved at once and the row redraws from the server, so it never shows stale values.
  const save = (bot_id: number, k: string[]) => void run("save", async () => {
    await api("/panel/mirrors/save", { id: m.id, bot_id, kinds: k })
    toast("Saved"); await onDone()
  })
  return (
    <TR>
      <TD className="pl-4">
        <div className="font-medium">{m.title || "Channel " + m.channel_id}</div>
        <div className="flex flex-wrap items-center gap-1.5 pt-0.5 text-xs text-muted-foreground">
          {m.username ? <a className="underline-offset-2 hover:underline" href={"https://t.me/" + m.username} target="_blank" rel="noreferrer">t.me/{m.username}</a> : <StatusBadge>private</StatusBadge>}
          <span>{m.sent} copied</span>
          {!botLive && <StatusBadge tone="warn">bot connects after restart</StatusBadge>}
          {m.error && botLive && <StatusBadge tone="bad">problem</StatusBadge>}
        </div>
        {m.error && botLive && <div className="pt-0.5 text-xs text-destructive">{m.error}</div>}
      </TD>
      <TD><KindPicks value={m.kinds} disabled={busy === "save"} onChange={(k) => { if (k.length) save(m.bot_id, k) }} /></TD>
      <TD><Select size="sm" label="Bot" value={String(m.bot_id)} onValueChange={(v) => save(Number(v), m.kinds)} options={botOptions(bots, m.bot_id)} /></TD>
      <TD className="pr-4">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="outline" disabled={busy === "vis"} onClick={() => void run("vis", async () => {
            const ok = await confirmDialog(m.username ? "Make this mirror private?" : "Make this mirror public?",
              m.username ? "The link t.me/" + m.username + " stops working. People already in the channel stay." : "Anyone with the link can join. A random username is chosen.", m.username ? "Make private" : "Make public", false)
            if (!ok) return
            await api("/panel/mirrors/visibility", { id: m.id, public: !m.username })
            toast(m.username ? "Now private" : "Now public"); await onDone()
          })}>{m.username ? "Make private" : "Make public"}</Button>
          <Button size="sm" variant="destructive" disabled={busy === "rm"} onClick={() => void run("rm", async () => {
            const ok = await confirmDialog("Remove this mirror?", "Copying stops. The channel and what is already in it stay in Telegram.", "Remove")
            if (!ok) return
            await api("/panel/mirrors/remove", { id: m.id }); await onDone()
          })}>Remove</Button>
        </div>
      </TD>
    </TR>
  )
}

// ---------- Shared library ----------

export function Shared() {
  const { data: s } = usePanel()
  const refresh = useRefreshState()
  const f = (s?.forum ?? null) as Forum | null
  const [name, setName] = useState("")
  const [found, setFound] = useState<{ user: string; fingerprint: string } | null>(null)
  const { busy, run } = useBusy()
  if (!f) return <Page title="Shared library" text="The shared library is not available in this run."><span /></Page>
  async function act(action: string, body: unknown) {
    const out = await api<{ message?: string; restart?: boolean }>("/panel/forum/" + action, body)
    toast(out.message || (out.restart ? "Saved. Restart to use the shared library." : "Saved"))
    await refresh()
  }
  const status = f.error ? "The shared library is off: " + f.error : f.fingerprint ? "Running." : f.group ? "Restart to start it." : ""
  const others = (f.trusted ?? []).filter((r) => !r.own)
  return (
    <Page title="Shared library" text="Several installations can use this one channel. A new file is posted again with a signed line, so the others can trust it.">
      {status && <Alert tone={f.error ? "bad" : f.fingerprint ? "info" : "warn"} action={<Button size="sm" variant="outline" disabled={busy === "recheck"} onClick={() => void run("recheck", () => act("recheck", {}))}><RefreshCw className="size-3.5" />Check again</Button>}>{status}</Alert>}
      <Section>
        <Row title="Repost new files with a signed caption"><SwitchBox label="Shared library" checked={!!f.group} onCheckedChange={(on) => void act("group", { on }).catch(fail)} /></Row>
        <Row title="Delete inbox files after they reach Ready"><SwitchBox label="Delete originals" checked={!!f.delete_originals} onCheckedChange={(on) => void act("originals", { on }).catch(fail)} /></Row>
        {f.fingerprint && <CopyField label="This installation" value={f.fingerprint} />}
      </Section>
      <Section title="Trusted installations" text="A key is believed once its posts have been checked against their files. Add one by hand by looking up the bot of the other installation.">
        {others.length ? (
          <Table>
            <THead><TR><TH>Key</TH><TH>Checked</TH><TH /></TR></THead>
            <TBody>{others.map((r) => (
              <TR key={r.fingerprint}>
                <TD><code className="text-xs">{r.fingerprint}</code>{r.manual && <div className="text-xs text-muted-foreground">trusted by hand</div>}</TD>
                <TD><StatusBadge tone="ok">{(r.verified ?? 0) + " right"}</StatusBadge> {!!r.wrong && <StatusBadge tone="bad">{r.wrong + " wrong"}</StatusBadge>}</TD>
                <TD className="text-right">{r.manual && <Button size="sm" variant="destructive" onClick={() => void run("rm", async () => {
                  if (await confirmDialog("Stop trusting this key?", "Posts signed with it are no longer believed.", "Remove")) await act("trust_remove", { fingerprint: r.fingerprint })
                })}>Remove</Button>}</TD>
              </TR>
            ))}</TBody>
          </Table>
        ) : <Empty>No other installation has posted yet.</Empty>}
        <div className="flex gap-2">
          <Input value={name} placeholder="@librarybot" onChange={(e) => { setName(e.target.value); setFound(null) }} />
          <Button size="sm" variant="outline" disabled={!name.trim() || busy === "lookup"} onClick={() => void run("lookup", async () => {
            const out = await api<{ fingerprint: string }>("/panel/forum/trust_lookup", { username: name.trim() })
            setFound({ user: name.trim(), fingerprint: out.fingerprint })
          })}>Look up</Button>
        </div>
        {found && (
          <Alert action={<Button size="sm" disabled={busy === "trust"} onClick={() => void run("trust", async () => { await act("trust_add", { username: found.user }); setFound(null); setName("") })}>Trust this key</Button>}>
            Fingerprint <code>{found.fingerprint}</code>. Trust it only if it matches what the other installation shows.
          </Alert>
        )}
      </Section>
    </Page>
  )
}

// ---------- Home videos ----------

type HomeView = { channel?: number; problem?: string; folders?: { path: string; files?: number; error?: string }[]; files?: { name: string; kind?: string; status: string; note?: string }[] }

const homeTone = (st: string): Tone => /error|fail|problem/i.test(st) ? "bad" : /sent|synced|done|in telegram/i.test(st) ? "ok" : "warn"

export function HomeVideos() {
  const q = useQuery({ queryKey: ["home"], queryFn: () => api<HomeView>("/panel/home"), refetchInterval: () => (pollingPaused() ? false : 8000) })
  const h = q.data
  const channel = useDirty(h?.channel ? "-100" + h.channel : "")
  const [folder, setFolder] = useState("")
  const { busy, run } = useBusy()
  if (!h) return <Page title="Home videos" text="Personal videos and photos.">{q.isError && <Alert tone="bad">{(q.error as Error).message}</Alert>}</Page>
  const files = h.files ?? []
  return (
    <Page title="Home videos" text="Personal videos and photos. They get their own Telegram channel, and a Jellyfin library called Home Videos and Photos.">
      {h.problem && <Alert tone="bad">{h.problem}</Alert>}
      <Section title="Channel" text="A private channel only for these files. Saving it needs a restart.">
        <div className="flex gap-2">
          <Input value={channel.value} onChange={(e) => channel.set(e.target.value)} placeholder="https://t.me/c/1234567890/5 or -100…" />
          <Button size="sm" disabled={!channel.dirty || busy === "ch"} onClick={() => void run("ch", async () => { await api("/panel/home/channel", { channel: channel.value }); channel.saved(); toast("Saved. Restart to use it.") })}>Save</Button>
        </div>
      </Section>
      <Section title="Folders" actions={<Button size="sm" variant="outline" disabled={busy === "sync"} onClick={() => void run("sync", async () => { const out = await api<{ queued?: number }>("/panel/home/sync", {}); toast(out.queued ? "Queued " + plural(out.queued, "file") : "Nothing new to send") })}>Sync into the channel</Button>}>
        {(h.folders ?? []).map((f) => (
          <div key={f.path} className="flex items-center justify-between gap-2">
            <div className="min-w-0"><code className="block truncate text-sm">{f.path}</code>{f.error ? <div className="text-xs text-destructive">{f.error}</div> : <div className="text-xs text-muted-foreground">{plural(f.files ?? 0, "file")}</div>}</div>
            <Button size="sm" variant="ghost" onClick={() => void run("rm", async () => {
              if (!(await confirmDialog("Remove this folder?", "Its files on this computer are not touched.", "Remove"))) return
              await api("/panel/home/folder", { path: f.path, remove: true }); await q.refetch()
            })}>Remove</Button>
          </div>
        ))}
        {!h.folders?.length && <Empty>No folder yet.</Empty>}
        <div className="flex gap-2">
          <Input value={folder} placeholder="D:\Home\Videos" onChange={(e) => setFolder(e.target.value)} />
          <Button size="sm" disabled={!folder.trim() || busy === "add"} onClick={() => void run("add", async () => { await api("/panel/home/folder", { path: folder.trim() }); setFolder(""); await q.refetch() })}>Add folder</Button>
        </div>
      </Section>
      {!!files.length && <Card>
        <Table>
          <THead><TR><TH className="pl-4">File</TH><TH className="pr-4 text-right">Status</TH></TR></THead>
          <TBody>{files.slice(0, 80).map((f, i) => (
            <TR key={f.name + i}><TD className="pl-4"><div className="truncate">{f.name}</div>{f.note && <div className="text-xs text-muted-foreground">{f.note}</div>}</TD><TD className="pr-4 text-right"><StatusBadge tone={homeTone(f.status)}>{f.status}</StatusBadge></TD></TR>
          ))}</TBody>
        </Table>
        {files.length > 80 && <Empty>And {files.length - 80} more.</Empty>}
      </Card>}
    </Page>
  )
}

// ---------- Subtitles ----------

export function Subtitles() {
  const { data: s } = usePanel()
  const refresh = useRefreshState()
  const draft = useDirty((s?.language || "").split(/[,;\s]+/).filter(Boolean))
  const { busy, run } = useBusy()
  if (!s) return null
  return (
    <Page title="Subtitles" text="Pick every language to keep. The first one is preferred; drag or use the arrows to change the order. Leave none to skip subtitles.">
      <Section>
        <MultiSelect value={draft.value} onChange={draft.set} />
        <div className="flex justify-end"><Button size="sm" disabled={!draft.dirty || busy === "save"} onClick={() => void run("save", async () => {
          await api("/panel/settings", { language: draft.value.join(",") })
          draft.saved(); toast("Saved. Restart to use it."); await refresh()
        })}>Save</Button></div>
      </Section>
    </Page>
  )
}

// ---------- Telegram account ----------

export function Telegram() {
  const { data: s } = usePanel()
  const refresh = useRefreshState()
  const [open, setOpen] = useState(false)
  const list = s?.telegram?.sessions ?? []
  return (
    <Page title="Telegram account" text="Sign in first. Torrents, Channels and the other tools open once an account is signed in. The account must be an admin of the library channel with permission to post.">
      <Card>
        <div className="flex items-center justify-between gap-2 border-b px-4 py-3">
          <h2 className="font-medium">Accounts</h2>
          <Button size="sm" onClick={() => setOpen(true)}>Add account</Button>
        </div>
        <Table>
          <TBody>{list.map((ses) => <SessionRow key={ses.user_id} ses={ses} onDone={refresh} />)}</TBody>
        </Table>
        {!list.length && <Empty>No Telegram account is signed in.</Empty>}
      </Card>
      <p className="text-sm text-muted-foreground">A file larger than the account can send is refused, not split. Sign-in works from this computer or over Tailscale only. The session is stored in data/users and is not encrypted, so keep that folder like a password.</p>
      <TelegramLoginDialog open={open} onOpenChange={setOpen} onDone={() => void refresh()} />
    </Page>
  )
}

function SessionRow({ ses, onDone }: { ses: Session; onDone: () => Promise<unknown> }) {
  const { busy, run } = useBusy()
  const name = ses.username ? "@" + ses.username : (ses.phone || "user " + ses.user_id)
  const cap = Math.round((ses.max_bytes || 0) / 1e6)
  return (
    <TR>
      <TD className="pl-4">
        <div className="font-medium">{name}</div>
        <div className="flex flex-wrap items-center gap-1 pt-1">
          <StatusBadge tone={ses.connected ? "ok" : "warn"}>{ses.connected ? "connected" : "not connected"}</StatusBadge>
          <StatusBadge ok={!!ses.admin}>{ses.admin ? "admin" : "not an admin"}</StatusBadge>
          <StatusBadge>{ses.premium ? "Premium" : "Standard"}</StatusBadge>
          {cap > 0 && <span className="text-xs text-muted-foreground">{cap} MB limit</span>}
          {ses.error && <StatusBadge tone="bad">{ses.error}</StatusBadge>}
        </div>
        {ses.busy && ses.total ? <div className="max-w-sm space-y-1 pt-2"><Progress value={((ses.progress || 0) / ses.total) * 100} /><div className="text-xs text-muted-foreground">Uploading {bytes(ses.progress || 0)} of {bytes(ses.total)}</div></div> : null}
      </TD>
      <TD className="pr-4 text-right">
        <Button size="sm" variant="ghost" disabled={busy === "out"} onClick={() => void run("out", async () => {
          if (!(await confirmDialog("Sign out " + name + "?", "The session on this computer is ended. Tools that need an account stop until one is signed in again.", "Sign out"))) return
          await api("/panel/downloads/logout", { user_id: ses.user_id }); toast("Signed out"); await onDone()
        })}>Sign out</Button>
      </TD>
    </TR>
  )
}

// ---------- Bots ----------

export function Bots() {
  const { data: s } = usePanel()
  const job = useJob()
  const refresh = useRefreshState()
  const [token, setToken] = useState("")
  const [count, setCount] = useState("1")
  const [picked, setPicked] = useState<string[]>([])
  const [mine, setMine] = useState(false)
  const { busy, run } = useBusy()
  const signed = (s?.telegram?.sessions?.length ?? 0) > 0
  if (!s) return null
  const running = !!job.data?.running
  const owned = job.data?.owned ?? []
  return (
    <Page title="Bots" text="A bot is an account that reads the videos. The token is its password. This program never shows the full token again.">
      <Card>
        <Table>
          <THead><TR><TH className="pl-4">Bot</TH><TH>Status</TH><TH /></TR></THead>
          <TBody>{s.bots.map((b) => (
            <TR key={b.id}>
              <TD className="pl-4"><div className="font-medium">{b.name}</div><div className="text-xs text-muted-foreground">token …{b.hint}</div></TD>
              <TD>
                <div className="flex flex-wrap gap-1">
                  {!b.connected ? <StatusBadge tone="warn">connects after restart</StatusBadge>
                    : b.ok && b.admin ? <StatusBadge tone="ok">admin</StatusBadge>
                      : <StatusBadge tone="bad">{b.error || "not an admin"}</StatusBadge>}
                </div>
              </TD>
              <TD className="pr-4 text-right">
                <Button size="sm" variant="ghost" disabled={s.bots.length < 2} title={s.bots.length < 2 ? "Keep at least one bot" : undefined} onClick={() => void run("rm" + b.id, async () => {
                  if (!(await confirmDialog("Remove " + b.name + "?", "Its token is deleted from the settings. Mirrors it posts to need another bot.", "Remove"))) return
                  await api("/panel/bots", { remove: b.id }); await refresh()
                })}>Remove</Button>
              </TD>
            </TR>
          ))}</TBody>
        </Table>
      </Card>

      <Section title="Create bots automatically" text="Makes new bots with @BotFather using your Telegram account and adds them as admins of the main channel. They connect after a restart.">
        {!signed ? <SignInFirst /> : <>
          <div className="flex items-end gap-2">
            <div className="w-32 space-y-1.5"><FieldLabel>How many</FieldLabel><Select label="How many bots" value={count} onValueChange={setCount} options={[["1", "1 bot"], ["2", "2 bots"], ["3", "3 bots"]]} /></div>
            <Button size="sm" className="h-9" disabled={running || busy === "create"} onClick={() => void run("create", async () => { await api("/panel/bots/create", { count: Number(count) }); setMine(true); await job.refetch() })}>{running ? "Working…" : "Create"}</Button>
          </div>
          {(mine || running) && <ProgressSteps lines={job.data?.progress ?? []} />}
          {mine && job.data?.error && <Alert tone="bad">{job.data.error}</Alert>}
          {mine && !!owned.length && <div className="space-y-2">
            <p className="text-sm">This account already has the most bots Telegram allows. Pick bots to reuse; their old token stops working.</p>
            <div className="grid gap-2 sm:grid-cols-2">{owned.map((b) => <Checkbox key={b.username} label={"@" + b.username + (b.name ? " · " + b.name : "")} checked={picked.includes(b.username)} onCheckedChange={(on) => setPicked(on ? [...picked, b.username] : picked.filter((n) => n !== b.username))} />)}</div>
            <div className="flex justify-end"><Button size="sm" disabled={!picked.length || picked.length > 3 || running} onClick={() => void run("reuse", async () => { await api("/panel/bots/reuse", { usernames: picked }); setPicked([]); await job.refetch() })}>Use the selected bots</Button></div>
          </div>}
        </>}
      </Section>

      <Section title="Add a bot" text="Already have a token? Paste it here.">
        <Collapse title="How to create a bot by hand">
          <ol className="list-decimal space-y-1 pl-4">
            <li>Open @BotFather in Telegram.</li>
            <li>Send /newbot.</li>
            <li>Choose a display name, then a username that ends in bot.</li>
            <li>Make the bot an admin of the library channel.</li>
            <li>Paste the token below. Do not post it in the channel.</li>
          </ol>
        </Collapse>
        <div className="flex gap-2">
          <Input value={token} placeholder="123456789:AAE..." onChange={(e) => setToken(e.target.value)} />
          <Button size="sm" className="h-9" disabled={!token.trim() || busy === "add"} onClick={() => void run("add", async () => { await api("/panel/bots", { add: token.trim() }); setToken(""); toast("Saved. Restart to connect it."); await refresh() })}>Add</Button>
        </div>
      </Section>
    </Page>
  )
}

// ---------- Telegram API ----------

export function ApiPage() {
  const { data: s } = usePanel()
  const [id, setId] = useState("")
  const [hash, setHash] = useState("")
  const [reveal, setReveal] = useState(false)
  const [show, setShow] = useState(false)
  const { busy, run } = useBusy()
  if (!s) return null
  const editing = show || !s.default_api
  return (
    <Page title="Telegram API" text="The API ID and API hash identify this program to Telegram. They are not the bot token.">
      <Section>
        {s.default_api && !show && <Row title="Default" text="You're using the default. Replace it here if you want your own."><Button size="sm" variant="outline" onClick={() => setShow(true)}>Replace</Button></Row>}
        {editing && <>
          <Collapse title="Where do I get these?">
            <p>Telegram does not offer a public ID you can download or copy from someone else. Each person creates a pair, free, with their own phone number. This program speaks Telegram's own protocol, and that protocol refuses a connection without this pair.</p>
            <p className="pt-2">Open my.telegram.org, then API development tools, and copy the numeric App api_id and the App api_hash.</p>
          </Collapse>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5"><FieldLabel htmlFor="api-id">API ID</FieldLabel><Input id="api-id" inputMode="numeric" value={id} placeholder={s.api_id ? String(s.api_id) : "api_id"} onChange={(e) => setId(e.target.value)} /></div>
            <div className="space-y-1.5"><FieldLabel htmlFor="api-hash">API hash</FieldLabel>
              <div className="relative">
                <Input id="api-hash" className="pr-9" type={reveal ? "text" : "password"} value={hash} placeholder={s.default_api ? "api_hash" : "Leave empty to keep the saved hash"} onChange={(e) => setHash(e.target.value)} />
                <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground" aria-label={reveal ? "Hide" : "Show"} onClick={() => setReveal(!reveal)}>{reveal ? <EyeOff className="size-4" /> : <Eye className="size-4" />}</button>
              </div>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            {s.default_api && <Button size="sm" variant="ghost" onClick={() => setShow(false)}>Cancel</Button>}
            <Button size="sm" disabled={busy === "save" || (!id.trim() && !hash.trim())} onClick={() => void run("save", async () => { await api("/panel/settings", { api_id: id, api_hash: hash }); setHash(""); toast("Saved. Restart to use it.") })}>Save</Button>
          </div>
        </>}
      </Section>
    </Page>
  )
}

// ---------- Direct files and News channels ----------

const lookbacks: [string, string][] = [["1", "1 day"], ["3", "3 days"], ["7", "1 week"], ["14", "2 weeks"], ["30", "1 month"], ["90", "3 months"], ["180", "6 months"], ["365", "1 year"], ["0", "No limit, all history"]]

type Rule = { enabled?: boolean; look_back_days?: number; every_minutes?: number; min_minutes?: number; min_mb?: number; max_gb?: number; max_per_day?: number; pause_seconds?: number; skip_words?: string; only_words?: string; need_title?: boolean }
type Src = { id: number; title?: string; username?: string; channel_id: number; enabled: boolean; strict?: boolean; no_forwards?: boolean; in_progress?: boolean; last_scan?: number; copied?: number; error?: string }
type Recent = { name?: string; url?: string; source_id?: number; size?: number; updated?: number; state?: string; error?: string; detail?: string }
type SourcesView = {
  settings?: Rule; running?: boolean; today?: number; note?: string; last_pass?: number; accounts?: { total?: number; healthy?: number }
  sources?: Src[]; recent?: Recent[]
  news?: { settings?: Rule; running?: boolean; note?: string; channels?: Src[]; recent?: Recent[] }
}

export function Sources() { return <RulesPage kind="direct" /> }
export function News() { return <RulesPage kind="news" /> }

const newsStates: Record<string, [string, Tone]> = { done: ["forwarded", "ok"], skipped: ["skipped", "warn"], gave_up: ["gave up", "bad"], fetched: ["forwarding", "info"], pending: ["opening", "info"] }

function srcStatus(src: Src, running: boolean): [string, Tone] {
  if (src.no_forwards) return ["copying forbidden", "bad"]
  if (src.error) return ["problem", "bad"]
  if (!src.enabled) return ["paused", "warn"]
  if (src.in_progress) return ["going through history", "info"]
  if (running && !src.last_scan) return ["checking now", "info"]
  if (src.last_scan) return ["up to date", "ok"]
  return ["waiting for the first check", "info"]
}

function RulesPage({ kind }: { kind: "direct" | "news" }) {
  const panel = usePanel()
  const news = kind === "news"
  const q = useQuery({ queryKey: ["sources"], queryFn: () => api<SourcesView>("/panel/sources"), refetchInterval: () => (pollingPaused() ? false : 5000) })
  const v = q.data
  const remote = news ? v?.news?.settings : v?.settings
  const draft = useDirty(remote)
  const [value, setValue] = useState("")
  const { busy, run } = useBusy()
  const signed = (panel.data?.telegram?.sessions?.length ?? 0) > 0
  const title = news ? "News channels" : "Direct files"
  if (!signed) return <Page title={title} text=""><SignInFirst /></Page>
  if (!v || !remote || !draft.value) return <Page title={title} text="">{q.isError && <Alert tone="bad">{(q.error as Error).message}</Alert>}</Page>
  const enabled = !!remote.enabled
  const running = !!(news ? v.news?.running : v.running)
  const note = news ? v.news?.note : v.note
  const list = (news ? v.news?.channels : v.sources) ?? []
  const recent = (news ? v.news?.recent : v.recent) ?? []
  const names: Record<number, string> = {}
  for (const s of list) names[s.id] = s.title || s.username || String(s.channel_id)
  const meta: string[] = []
  if (enabled) meta.push(running ? "Checking now" : "Next check within " + (remote.every_minutes ?? 0) + " minutes")
  if (!news) meta.push((v.today ?? 0) + " copied today" + (remote.max_per_day ? " of " + remote.max_per_day : ""))
  if (!news && v.last_pass) meta.push("Last check " + ago(v.last_pass))
  const act = (action: string) => "/panel/sources/" + (news ? "news_" + action : action)
  const toggleOn = async (on: boolean) => {
    await api(news ? "/panel/sources/news" : "/panel/sources/settings", { enabled: on })
    toast(on ? "On. The first check starts now." : "Off. What was copied stays in the library.")
    await q.refetch()
  }
  const look = draft.value.look_back_days ?? 0
  const lookChoices = lookbacks.some(([k]) => k === String(look)) ? lookbacks : [...lookbacks, [String(look), look + " days"] as [string, string]]
  return (
    <Page title={title} text={news ? "These channels post a bot link instead of the film. Any free signed-in account opens the link and forwards the file into the library." : "Give the signed-in Telegram account other channels that post the film itself. New files are copied into the library."}>
      <Section>
        <Row title={news ? (enabled ? "Opening bot links is on" : "Opening bot links is off") : (enabled ? "Copying from channels is on" : "Copying from channels is off")} text={meta.join(" · ")}>
          <SwitchBox label="Enabled" checked={enabled} onCheckedChange={(on) => void toggleOn(on).catch(fail)} />
        </Row>
        {!news && !(v.accounts?.healthy) && <Alert tone="warn">No signed-in account can read channels right now.</Alert>}
        {note && <Alert>{note}</Alert>}
      </Section>

      <Card>
        <div className="flex flex-wrap items-center gap-2 border-b px-4 py-3">
          <Input className="min-w-48 flex-1" value={value} placeholder="@channel or -100…" onChange={(e) => setValue(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") (e.currentTarget.nextElementSibling as HTMLButtonElement | null)?.click() }} />
          <Button size="sm" className="h-9" disabled={!value.trim() || busy === "add"} onClick={() => void run("add", async () => {
            const res = await api<{ source?: { no_forwards?: boolean } }>(act("add"), { channel: value.trim() })
            setValue("")
            if (res.source?.no_forwards) toast.warning("Added, but its owner forbids copying files out of it.")
            else toast("Added")
            await q.refetch()
            if (!enabled && await confirmDialog(news ? "Turn news channels on?" : "Turn direct file channels on?", "The channel is saved. Nothing is copied until this is on.", "Turn on", false)) await toggleOn(true)
          })}>Add channel</Button>
          {!news && <>
            <Button size="sm" variant="outline" className="h-9" onClick={() => void run("check", async () => { await api("/panel/sources/check", {}); toast("Checking"); setTimeout(() => void q.refetch(), 1200) })}>Check now</Button>
            <Button size="sm" variant="outline" className="h-9" onClick={() => void run("rescan", async () => { await api("/panel/sources/rescan", {}); toast("Every channel will be looked at again"); setTimeout(() => void q.refetch(), 1200) })}>Look again</Button>
          </>}
        </div>
        <Table>
          <TBody>{list.map((src) => {
            const [label, tone] = srcStatus(src, running)
            return (
              <TR key={src.id}>
                <TD className="pl-4">
                  <div className="font-medium">{src.title || "Channel " + src.channel_id}</div>
                  <div className="flex flex-wrap items-center gap-1.5 pt-0.5 text-xs text-muted-foreground">
                    <span>{src.username ? "@" + src.username : "-100" + src.channel_id}</span>
                    <StatusBadge tone={tone}>{label}</StatusBadge>
                    <span>{(src.copied ?? 0) + (news ? " forwarded" : " copied")}</span>
                    {src.last_scan ? <span>checked {ago(src.last_scan)}</span> : null}
                  </div>
                  {src.error && <div className="pt-0.5 text-xs text-destructive">{src.error}</div>}
                </TD>
                <TD className="pr-4">
                  <div className="flex justify-end gap-1">
                    {!src.no_forwards && <Button size="sm" variant="ghost" onClick={() => void run("p" + src.id, async () => { await api(act("pause"), { id: src.id, enabled: !src.enabled }); await q.refetch() })}>{src.enabled ? "Pause" : "Resume"}</Button>}
                    {!news && <Button size="sm" variant="ghost" title="Strict keeps only files whose name shows a year or an episode number. Use it for a channel that posts everything." onClick={() => void run("s" + src.id, async () => { await api("/panel/sources/strict", { id: src.id, strict: !src.strict }); await q.refetch() })}>{src.strict ? "Strict: on" : "Strict: off"}</Button>}
                    <Button size="sm" variant="ghost" className="text-destructive" onClick={() => void run("r" + src.id, async () => {
                      if (!(await confirmDialog("Remove this channel?", "Nothing more is copied from it. What is already in the library stays.", "Remove"))) return
                      await api(act("remove"), { id: src.id }); toast("Channel removed"); await q.refetch()
                    })}>Remove</Button>
                  </div>
                </TD>
              </TR>
            )
          })}</TBody>
        </Table>
        {!list.length && <Empty>{news ? "No news channel yet." : "No channel yet. Add one above."}</Empty>}
      </Card>

      <Section title="What to copy">
        <Collapse title="How do I get a channel id?">
          <p>A public channel is @name. For a private one, forward a message to @id_bot or @getidsbot. An invite link does not work.</p>
        </Collapse>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <div className="space-y-1.5"><FieldLabel>Look back</FieldLabel><Select label="Look back" value={String(draft.value.look_back_days ?? 0)} onValueChange={(x) => draft.set({ ...draft.value!, look_back_days: Number(x) })} options={lookChoices} /></div>
          <Num label="Check every, minutes" k="every_minutes" draft={draft} />
          <Num label="Shortest video, minutes" k="min_minutes" draft={draft} />
          <Num label="Smallest file, MB" k="min_mb" draft={draft} />
          <Num label="Largest file, GB" k="max_gb" draft={draft} />
          <Num label="Most videos per day" k="max_per_day" draft={draft} />
          <Num label="Pause between files, seconds" k="pause_seconds" draft={draft} />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5"><FieldLabel>Skip words</FieldLabel><Input value={draft.value.skip_words || ""} placeholder="trailer, sample" onChange={(e) => draft.set({ ...draft.value!, skip_words: e.target.value })} /></div>
          <div className="space-y-1.5"><FieldLabel>Only words</FieldLabel><Input value={draft.value.only_words || ""} onChange={(e) => draft.set({ ...draft.value!, only_words: e.target.value })} /></div>
        </div>
        <Row title="Only files that look like a film or an episode"><SwitchBox label="Need a title" checked={!!draft.value.need_title} onCheckedChange={(need_title) => draft.set({ ...draft.value!, need_title })} /></Row>
        <div className="flex justify-end gap-2">
          {draft.dirty && <Button size="sm" variant="ghost" onClick={() => draft.saved()}>Discard</Button>}
          <Button size="sm" disabled={!draft.dirty || busy === "rules"} onClick={() => void run("rules", async () => {
            const { enabled: _skip, ...rules } = draft.value!
            void _skip
            await api(act("settings"), rules); draft.saved(); toast("Rules saved"); await q.refetch()
          })}>Save rules</Button>
        </div>
      </Section>

      <Card>
        <div className="border-b px-4 py-3"><h2 className="font-medium">Recent</h2></div>
        <Table>
          <TBody>{recent.map((r, i) => {
            const [label, tone]: [string, Tone] = news ? (newsStates[r.state || ""] ?? [r.state || "waiting", "info"]) : (r.state === "copied" ? ["copied", "ok"] : ["skipped", "warn"])
            const extra = [r.source_id !== undefined ? names[r.source_id] : "", !news && r.size ? bytes(r.size) : "", r.updated ? ago(r.updated) : "", r.error || r.detail || ""].filter(Boolean).join(" · ")
            return (
              <TR key={i}>
                <TD className="pl-4"><div className="max-w-xl truncate">{news ? r.url : r.name}</div><div className="text-xs text-muted-foreground">{extra}</div></TD>
                <TD className="pr-4 text-right"><StatusBadge tone={tone}>{label}</StatusBadge></TD>
              </TR>
            )
          })}</TBody>
        </Table>
        {!recent.length && <Empty>{news ? "Nothing opened yet." : "Nothing copied yet."}</Empty>}
      </Card>
    </Page>
  )
}

function Num({ label, k, draft }: { label: string; k: keyof Rule; draft: { value: Rule | undefined; set: (v: Rule) => void } }) {
  return <div className="space-y-1.5"><FieldLabel>{label}</FieldLabel><Input type="number" min={0} value={Number(draft.value?.[k] ?? 0)} onChange={(e) => draft.set({ ...draft.value, [k]: Number(e.target.value) })} /></div>
}

// ---------- Local files ----------

type LocalFile = { id: number; name: string; kind?: string; title?: string; year?: number; season?: number; number?: number; rel?: string; size: number; status: string; state?: string; note?: string; progress?: number; total?: number }
type LocalGroup = { title: string; year?: number; bytes?: number; files?: LocalFile[] }
type Totals = { count?: number; bytes?: number }
type LocalView = { problem?: string; scanning?: boolean; scanned?: number; folders?: { path: string; files?: number; bytes?: number; error?: string }[]; totals?: { local?: Totals; busy?: Totals; synced?: Totals; problem?: Totals }; categories?: { id: string; label: string; groups?: LocalGroup[] }[] }
type LocalResult = { done?: number; bytes?: number; skipped?: number; errors?: string[] }

function bucket(status: string) {
  if (status === "waiting" || status === "uploading" || status === "checking") return "busy"
  return status
}

const localChips: [string, string][] = [["all", "All"], ["local", "Only here"], ["busy", "On the way"], ["synced", "In Telegram"], ["problem", "Needs a look"]]

export function Local() {
  const q = useQuery({ queryKey: ["local"], queryFn: () => api<LocalView>("/panel/local"), refetchInterval: () => (pollingPaused() ? false : 4000) })
  const [path, setPath] = useState("")
  const [filter, setFilter] = useState("all")
  const { busy, run } = useBusy()
  const v = q.data
  if (!v) return <Page title="Local files" text="Videos already on this computer.">{q.isError && <Alert tone="bad">{(q.error as Error).message}</Alert>}</Page>
  const files = (v.categories ?? []).flatMap((c) => (c.groups ?? []).flatMap((g) => g.files ?? []))
  const only = files.filter((f) => f.status === "local")
  const synced = files.filter((f) => f.status === "synced")
  const sum = (l: LocalFile[]) => l.reduce((n, f) => n + (f.size || 0), 0)
  const keep = (f: LocalFile) => filter === "all" || bucket(f.status) === filter
  const t = v.totals ?? {}

  async function upload(ids: number[]) {
    const res = await api<LocalResult>("/panel/local/upload", { ids })
    if (res.done) toast("Queued " + plural(res.done, "file") + " (" + bytes(res.bytes ?? 0) + ")" + (res.skipped ? ". " + res.skipped + " skipped, already sent or waiting." : ""))
    else toast("Nothing to queue. " + (res.skipped ?? 0) + " were already sent or waiting.")
    await q.refetch()
  }
  async function remove(list: LocalFile[], label: string) {
    const what = list.length === 1 ? "“" + label + "”" : plural(list.length, "file") + (label ? " of " + label : "")
    const ok = await confirmDialog("Delete from this computer?", "This deletes " + what + " (" + bytes(sum(list)) + ") from this computer for good. " + (list.length === 1 ? "It is" : "Each one is") + " in the Telegram channel, and Jellyfin has read it from there. The channel copy stays.", list.length === 1 ? "Delete the local copy" : "Delete " + list.length + " local copies")
    if (!ok) return
    report(await api<LocalResult>("/panel/local/delete", { ids: list.map((f) => f.id) }))
    await q.refetch()
  }
  async function cancel(id: number) {
    await api("/panel/local/cancel", { ids: [id] }); toast("Taken out of the line"); await q.refetch()
  }
  const actions = { upload: (ids: number[]) => run("u", () => upload(ids)), remove: (l: LocalFile[], label: string) => run("d", () => remove(l, label)), cancel: (id: number) => run("c", () => cancel(id)) }

  return (
    <Page title="Local files" text="Videos already on this computer, sorted into films and series. Nothing is uploaded or deleted until you press a button.">
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Only on this computer" value={String(t.local?.count ?? 0)} sub={t.local?.count ? bytes(t.local.bytes ?? 0) + " to free once uploaded" : "Nothing is waiting"} />
        <Stat label="On the way" value={String(t.busy?.count ?? 0)} sub={t.busy?.count ? bytes(t.busy.bytes ?? 0) : "Nothing is being sent"} />
        <Stat label="In Telegram" value={String(t.synced?.count ?? 0)} sub={t.synced?.count ? bytes(t.synced.bytes ?? 0) + " can be deleted from here" : "Nothing yet"} />
        {!!t.problem?.count && <Stat label="Needs a look" value={String(t.problem.count)} sub="Open the list to see why" />}
      </div>
      {v.problem && <Alert tone="warn" action={<Button size="sm" variant="outline" asChild><Link to="/telegram">Add account</Link></Button>}>{v.problem}</Alert>}

      <Section title="Folders" text={v.scanning ? "Scanning…" : v.scanned ? "Scanned at " + new Date(v.scanned * 1000).toLocaleTimeString() : undefined}
        actions={<Button size="sm" variant="outline" disabled={busy === "scan" || v.scanning} onClick={() => void run("scan", async () => { await api("/panel/local/scan", {}); toast("Scanned"); await q.refetch() })}><RefreshCw className="size-3.5" />Scan now</Button>}>
        {(v.folders ?? []).map((f) => (
          <div key={f.path} className="flex items-center justify-between gap-2">
            <div className="min-w-0"><code className="block truncate text-sm">{f.path}</code>
              {f.error ? <div className="text-xs text-destructive">{f.error}</div> : <div className="text-xs text-muted-foreground">{plural(f.files ?? 0, "video")} · {bytes(f.bytes ?? 0)}</div>}</div>
            <Button size="sm" variant="ghost" onClick={() => void run("rmf", async () => {
              if (!(await confirmDialog("Remove this folder?", "Its files are not touched. They just stop showing here.", "Remove"))) return
              await api("/panel/local/folder", { remove: f.path }); toast("Folder removed. Its files were not touched."); await q.refetch()
            })}>Remove</Button>
          </div>
        ))}
        {!v.folders?.length && <Empty>No folder yet. Add the folder that holds your films or series.</Empty>}
        <div className="flex gap-2">
          <Input value={path} placeholder="D:\Library\Movie" onChange={(e) => setPath(e.target.value)} />
          <Button size="sm" className="h-9" disabled={!path.trim() || busy === "addf"} onClick={() => void run("addf", async () => { await api("/panel/local/folder", { add: path.trim() }); setPath(""); toast("Folder added. Scanning it now."); setTimeout(() => void q.refetch(), 1500) })}>Add folder</Button>
        </div>
      </Section>

      <Section>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Toggles value={filter} onChange={setFilter} options={localChips.filter(([k]) => k !== "problem" || files.some((f) => f.status === "problem") || filter === "problem").map(([k, label]) => [k, label + " " + (k === "all" ? files.length : files.filter((f) => bucket(f.status) === k).length)])} />
          <div className="flex flex-wrap gap-2">
            <Button size="sm" disabled={!only.length || !!v.problem || busy === "u"} onClick={() => void actions.upload(only.map((f) => f.id))}>
              Upload everything that is only here{only.length ? " (" + only.length + ", " + bytes(sum(only)) + ")" : ""}
            </Button>
            <Button size="sm" variant="destructive" disabled={!synced.length || busy === "d"} onClick={() => void run("d", async () => {
              const ok = await confirmDialog("Delete from this computer?", "This deletes " + plural(synced.length, "local copy").replace("copys", "copies") + " (" + bytes(sum(synced)) + ") that are already in Telegram. The channel copies stay.", "Delete " + synced.length)
              if (!ok) return
              report(await api<LocalResult>("/panel/local/delete_synced", {})); await q.refetch()
            })}>Delete every local copy that is in Telegram{synced.length ? " (" + synced.length + ")" : ""}</Button>
          </div>
        </div>
        <div className="space-y-2">
          {(v.categories ?? []).map((c) => {
            const groups = (c.groups ?? []).map((g) => ({ ...g, files: (g.files ?? []).filter(keep) })).filter((g) => g.files.length)
            if (!groups.length) return null
            const shown = groups.flatMap((g) => g.files)
            return (
              <Disclosure key={c.id + filter} defaultOpen title={c.label} meta={plural(shown.length, "file") + " · " + bytes(sum(shown))}>
                {groups.map((g) => g.files.length === 1 && c.id !== "shows"
                  ? <FileRow key={g.title + g.files[0]!.id} f={g.files[0]!} actions={actions} />
                  : <GroupBlock key={g.title + (g.year ?? "")} cat={c.id} g={g} actions={actions} />)}
              </Disclosure>
            )
          })}
          {!files.filter(keep).length && <Empty>{v.folders?.length ? (filter === "all" ? "No videos found in these folders yet." : "Nothing here.") : "Add a folder above to see its videos."}</Empty>}
        </div>
      </Section>
    </Page>
  )
}

function report(res: LocalResult) {
  const errs = res.errors ?? []
  if (res.done) toast("Deleted " + plural(res.done, "file") + " and freed " + bytes(res.bytes ?? 0) + (errs.length ? ". Kept " + errs.length + ": " + errs.slice(0, 2).join(" ") : ""))
  else toast.error("Nothing was deleted." + (errs.length ? " " + errs.slice(0, 3).join(" ") : ""))
}

type LocalActions = { upload: (ids: number[]) => unknown; remove: (l: LocalFile[], label: string) => unknown; cancel: (id: number) => unknown }

function GroupBlock({ cat, g, actions }: { cat: string; g: LocalGroup & { files: LocalFile[] }; actions: LocalActions }) {
  const name = g.title + (g.year ? " (" + g.year + ")" : "")
  const only = g.files.filter((f) => f.status === "local")
  const synced = g.files.filter((f) => f.status === "synced")
  return (
    <Disclosure level={2} title={name}
      meta={plural(g.files.length, cat === "shows" ? "episode" : "file") + " · " + bytes(g.bytes ?? g.files.reduce((n, f) => n + f.size, 0)) + (synced.length ? " · " + synced.length + " in Telegram" : "")}
      actions={<>
        {!!only.length && <Button size="sm" variant="outline" onClick={() => void actions.upload(only.map((f) => f.id))}>Upload {only.length}</Button>}
        {!!synced.length && <Button size="sm" variant="ghost" className="text-destructive" onClick={() => void actions.remove(synced, name)}>Delete {synced.length} local</Button>}
      </>}>
      {g.files.map((f) => <FileRow key={f.id} f={f} actions={actions} />)}
    </Disclosure>
  )
}

function FileRow({ f, actions }: { f: LocalFile; actions: LocalActions }) {
  let title = f.name
  if (f.kind === "movie" && f.title) title = f.title + (f.year ? " (" + f.year + ")" : "")
  else if (f.kind === "episode") title = "S" + String(f.season ?? 0).padStart(2, "0") + "E" + String(f.number ?? 0).padStart(2, "0") + " · " + f.name
  const btn = (text: string, fn: () => unknown, danger = false) => <Button size="sm" variant={danger ? "ghost" : "outline"} className={danger ? "text-destructive" : undefined} onClick={() => void fn()}>{text}</Button>
  let side: ReactNode = null
  switch (f.status) {
    case "local": side = <><StatusBadge>Only here</StatusBadge>{btn("Upload", () => actions.upload([f.id]))}</>; break
    case "waiting": side = <><StatusBadge tone="info">Waiting</StatusBadge>{btn("Cancel", () => actions.cancel(f.id))}</>; break
    case "uploading": side = <StatusBadge tone="info">Uploading</StatusBadge>; break
    case "checking": side = <StatusBadge tone="info">In Telegram, checking</StatusBadge>; break
    case "synced": side = <><StatusBadge tone="ok">In Telegram</StatusBadge>{btn("Delete local copy", () => actions.remove([f], f.name), true)}</>; break
    case "problem": side = <><StatusBadge tone="bad">Needs a look</StatusBadge>{f.state === "mismatch" && btn("Upload again", () => actions.upload([f.id]))}{btn("Cancel", () => actions.cancel(f.id))}</>; break
  }
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 border-t py-2 first:border-t-0">
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm">{title}</div>
        <div className="truncate text-xs text-muted-foreground">{[f.rel, bytes(f.size)].filter(Boolean).join(" · ")}</div>
        {f.note && <div className={cn("text-xs", f.status === "problem" ? "text-destructive" : "text-muted-foreground")}>{f.note}</div>}
        {f.status === "uploading" && f.total ? <div className="max-w-sm space-y-1 pt-1"><Progress value={((f.progress ?? 0) / f.total) * 100} /><div className="text-xs text-muted-foreground">{bytes(f.progress ?? 0)} of {bytes(f.total)}</div></div> : null}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">{side}</div>
    </div>
  )
}

// ---------- Torrents ----------

type DL = {
  enabled?: boolean; dir?: string; free_bytes?: number; error?: string; warning?: string; pace?: Record<string, number | string>
  search_every_minutes?: number; last_search?: number; crawler?: boolean
  containers?: { name: string; running: boolean }[]; queue?: { name?: string; state?: string; error?: string }[]
}

function num(pace: Record<string, number | string> | undefined, a: string, b: string) {
  return Number(pace?.[a] ?? pace?.[b] ?? 0)
}

export function Downloads() {
  const { data: s } = usePanel()
  const refresh = useRefreshState()
  const d = (s?.downloads ?? null) as DL | null
  const pace = useDirty({
    mb_per_sec: num(d?.pace, "mb_per_sec", "MBPerSec"),
    rest_minutes: num(d?.pace, "rest_minutes", "RestMinutes"),
    long_every_gb: num(d?.pace, "long_every_gb", "LongEveryGB"),
    long_rest_minutes: num(d?.pace, "long_rest_minutes", "LongRestMinutes"),
    daily_gb: num(d?.pace, "daily_gb", "DailyGB"),
    hours: String(d?.pace?.hours ?? d?.pace?.Hours ?? ""),
  })
  const { busy, run } = useBusy()
  if (!d) return null
  const field = (label: string, k: keyof typeof pace.value, placeholder?: string) => (
    <div className="space-y-1.5"><FieldLabel>{label}</FieldLabel><Input value={String(pace.value[k])} placeholder={placeholder} inputMode={k === "hours" ? undefined : "decimal"}
      onChange={(e) => pace.set({ ...pace.value, [k]: k === "hours" ? e.target.value : Number(e.target.value) })} /></div>
  )
  const queueTone = (st: string, err?: string): Tone => (/err|fail|stall/i.test(st) || err ? "bad" : /done|complete|upload|seed/i.test(st) ? "ok" : "info")
  return (
    <Page title="Torrents" text="One switch for the whole chain. It is off until you turn it on.">
      <Section>
        <Row title={d.enabled ? "Torrent downloads are on" : "Torrent downloads are off"} text={[d.dir, d.free_bytes ? (d.free_bytes / 1024 ** 3).toFixed(1) + " GiB free" : ""].filter(Boolean).join(" · ")}>
          <SwitchBox label="Torrent downloads" checked={!!d.enabled} onCheckedChange={(enabled) => void api("/panel/automation", { enabled }).then(() => { toast(enabled ? "Starting. Playback is not interrupted." : "Stopped. Files already in the channel still play."); return refresh() }).catch(fail)} />
        </Row>
        {d.enabled && <Alert tone="warn">Torrents connect this computer to strangers and can be illegal where you live. Your address is visible to other people in the swarm. Leave this off unless you accept that.</Alert>}
      </Section>
      <Section title="Pace" text="How fast and when files go to Telegram, so the account is not flagged.">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {field("Megabytes per second", "mb_per_sec")}
          {field("Minutes between files", "rest_minutes")}
          {field("Long rest after this many GB", "long_every_gb")}
          {field("Long rest, minutes", "long_rest_minutes")}
          {field("Daily GB per account", "daily_gb")}
          {field("Hours", "hours", "01-08")}
        </div>
        <div className="flex justify-end"><Button size="sm" disabled={!pace.dirty || busy === "pace"} onClick={() => void run("pace", async () => { await api("/panel/downloads/pace", pace.value); pace.saved(); toast("Pace saved") })}>Save pace</Button></div>
      </Section>
      <Section title="Stack">
        {d.error && <Alert tone="bad">{d.error}</Alert>}
        {d.warning && <Alert tone="warn">{d.warning}</Alert>}
        {!!d.search_every_minutes && <p className="text-sm text-muted-foreground">{d.last_search ? "Looked again for missing titles at " + new Date(d.last_search * 1000).toLocaleTimeString() + ", then every " + d.search_every_minutes + " minutes." : "Will look again for missing titles every " + d.search_every_minutes + " minutes."}</p>}
        {(d.enabled || !!d.containers?.length) && <Row title={d.crawler ? "Bitmagnet is crawling the DHT." : "Bitmagnet search is on. The DHT crawler is paused because the database reached its cap."}><StatusBadge tone={d.crawler ? "ok" : "warn"}>{d.crawler ? "crawling" : "paused"}</StatusBadge></Row>}
        {(d.containers ?? []).map((c) => <Row key={c.name} title={c.name}><StatusBadge ok={c.running}>{c.running ? "running" : "stopped"}</StatusBadge></Row>)}
        {!d.enabled && !d.containers?.length && <Empty>Nothing is running yet.</Empty>}
      </Section>
      <Card>
        <div className="border-b px-4 py-3"><h2 className="font-medium">Queue</h2></div>
        <Table><TBody>{(d.queue ?? []).map((item, i) => (
          <TR key={i}><TD className="pl-4"><div className="truncate">{item.name}</div>{item.error && <div className="text-xs text-destructive">{item.error}</div>}</TD><TD className="pr-4 text-right"><StatusBadge tone={queueTone(item.state || "", item.error)}>{item.state || "queued"}</StatusBadge></TD></TR>
        ))}</TBody></Table>
        {!d.queue?.length && <Empty>Nothing queued.</Empty>}
      </Card>
    </Page>
  )
}

// ---------- Away from home ----------

export function Away() {
  const { data: s } = usePanel()
  return (
    <Page title="Away from home" text="At home, open an address tagged At home. Away from home, use Tailscale. It is a private network between your own devices. Nothing is published on the public internet.">
      <Section title="Set up Tailscale">
        <ol className="list-decimal space-y-1 pl-5 text-sm">
          <li>Install Tailscale on this computer.</li>
          <li>Install it on the phone or the TV.</li>
          <li>Sign both devices in to the same Tailscale account.</li>
          <li>Open the address tagged Away from home below.</li>
          <li>Do not turn on Tailscale Funnel. Funnel would publish Jellyfin to the whole internet, and it is a poor fit for video. No router port has to be opened.</li>
        </ol>
      </Section>
      <Card>
        <Table><TBody>{(s?.addresses ?? []).map((a) => (
          <TR key={a.url}>
            <TD className="w-36 pl-4"><StatusBadge tone={a.away ? "info" : "ok"}>{a.away ? "Away from home" : "At home"}</StatusBadge></TD>
            <TD className="pr-4"><CopyField label="" value={a.url} /></TD>
          </TR>
        ))}</TBody></Table>
        {!s?.addresses?.length && <Empty>No network address yet.</Empty>}
      </Card>
    </Page>
  )
}
