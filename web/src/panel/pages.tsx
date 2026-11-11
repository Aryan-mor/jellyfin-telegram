import { useEffect, useState, type ReactNode } from "react"
import { Link, useLocation } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Eye, EyeOff, RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { api, panelCode, pollingPaused } from "@/lib/api"
import { t } from "@/lib/i18n"
import { ago, bytes, cn, plural } from "@/lib/utils"
import {
  Alert, Button, Card, CardBody, Checkbox, Collapse, Disclosure, Empty, FieldLabel, Input, Progress, Select,
  SwitchBox, Table, TBody, TD, TH, THead, Toggles, TR,
} from "@/components/ui"
import { AutoFilterForm } from "@/components/autofilter"
import { confirmDialog, CopyField, MultiSelect, ProgressSteps, StatusBadge, TelegramLoginDialog, type Tone } from "@/components/widgets"
import { useJob, usePanel, useAutomation, type PanelState, type Session } from "@/panel/shell"

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
  toast.error(e instanceof Error ? e.message : t("Something went wrong"))
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

function channelID(id: number) {
  return id ? "-100" + id : ""
}

function ChannelName({ name, id, className }: { name?: string; id: number; className?: string }) {
  const tip = channelID(id)
  return <div className={className} title={tip || undefined}>{name?.trim() || tip || t("Channel")}</div>
}

function Stat({ label, value, sub, to, tip }: { label: string; value: string; sub: string; to?: string; tip?: string }) {
  const { search } = useLocation()
  const body = <><div className="text-xs text-muted-foreground">{label}</div><div className="truncate text-lg font-semibold" title={tip}>{value}</div><div className="truncate text-xs text-muted-foreground">{sub}</div></>
  if (to) return <Link to={{ pathname: to, search }} className="rounded-lg border bg-card p-3 transition-colors hover:bg-accent/50">{body}</Link>
  return <div className="rounded-lg border bg-card p-3">{body}</div>
}

function SignInFirst() {
  const { search } = useLocation()
  return <Alert tone="warn" action={<Button size="sm" variant="outline" asChild><Link to={{ pathname: "/telegram", search }}>{t("Accounts")}</Link></Button>}>{t("No Telegram account is signed in. Sign one in first.")}</Alert>
}

// ---------- Overview ----------

type Forum = { group?: boolean; delete_originals?: boolean; fingerprint?: string; error?: string; trusted?: { fingerprint: string; own?: boolean; manual?: boolean; verified?: number; wrong?: number }[] }

export function Overview() {
  const { data: s } = usePanel()
  const auto = useAutomation()
  if (!s) return null
  const signed = (s.telegram?.sessions?.length ?? 0) > 0
  const botsOk = s.bots.filter((b) => b.ok && b.admin).length
  const phone = s.addresses.find((a) => !a.away && !/localhost|127\.0\.0\.1/.test(a.url))
  const share = phone ? phone.url.replace(/\/$/, "") + "/panel?code=" + panelCode() : ""
  const forum = s.forum as Forum | null
  const mirrors = s.mirrors?.length ?? 0
  const apps = appsOf(s)
  let forumValue = t("Off"), forumSub = t("Plain channel")
  if (forum?.error) { forumValue = t("Problem"); forumSub = t("Open Shared library") }
  else if (forum?.fingerprint) { forumValue = t("On"); forumSub = t("Key {{fingerprint}}", { fingerprint: forum.fingerprint }) }
  else if (forum?.group) { forumValue = t("Waiting"); forumSub = t("Restart to start it") }
  return (
    <Page title={t("Overview")} text={t("Everything this server runs, and where to open it.")}>
      <Section
        title={t("Automatic work")}
        text={t("Bots and signed-in accounts stop searching, copying and uploading on their own. Playback, Jellyfin and anything you start here keep working.")}
        actions={<Button disabled={auto.busy} onClick={() => void auto.setPaused(!s.paused)}>{s.paused ? t("Start again") : t("Pause automatic work")}</Button>}
      >
        <p className="text-sm">{s.paused ? t("Paused. Bots and accounts are not working on their own.") : t("Running. Bots and accounts are working on their own.")}</p>
      </Section>
      {!signed && <SignInFirst />}
      <Section title={t("Apps")} text={t("User and password for each app. The name opens that app in a new tab.")}>
        <div className="grid gap-3 sm:grid-cols-2">
          {apps.map((a) => (
            <div key={a.app} className="space-y-2 rounded-lg border p-3">
              <a className="text-base font-semibold underline-offset-2 hover:underline" href={a.path} target="_blank" rel="noreferrer">{a.app}</a>
              {a.note && <p className="text-xs text-muted-foreground">{a.note}</p>}
              {a.user ? <>
                <CopyField label={t("User")} value={a.user} />
                <CopyField label={t("Password")} value={a.password || ""} />
              </> : <StatusBadge>{t("your own account")}</StatusBadge>}
            </div>
          ))}
        </div>
        {!apps.length && <Empty>{t("No apps to open yet.")}</Empty>}
      </Section>
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label={t("Bots")} value={botsOk + " / " + s.bots.length} sub={botsOk === s.bots.length ? t("All ready") : t("Some need a look")} to="/bots" />
        <Stat label={t("Channel")} value={s.channel_title || channelID(s.channel_id)} tip={channelID(s.channel_id)} sub={(s.language ? t("Subtitles {{language}}", { language: s.language }) : t("No subtitles")) + (mirrors ? " · " + plural(mirrors, "mirror") : "")} to="/channel" />
        <Stat label={t("Shared library")} value={forumValue} sub={forumSub} to="/channel/shared" />
      </div>
      <Section title={t("Open from another device")} text={t("On this computer, open /panel. From a phone on the same Wi-Fi, open this link. The part after code= is the secret that keeps the panel private.")}>
        <CopyField label={t("Share link")} value={share || t("No network address yet")} />
      </Section>
    </Page>
  )
}

type AppAccount = { app: string; path: string; user?: string; password?: string; note?: string }

function appsOf(s: PanelState): AppAccount[] {
  const list: AppAccount[] = (s.accounts ?? []).slice()
  if (!list.length) {
    list.push({ app: "Jellyfin", path: "/", note: t("Watch your library") })
    if (s.seerr) list.push({ app: "Seerr", path: "/seerr/", note: t("Request films and series") })
  }
  return list
}

// ---------- Sign in details ----------

export function Accounts() {
  const { data: s } = usePanel()
  const list = s ? appsOf(s) : []
  return (
    <Page title={t("Sign in details")} text={t("The account to use in each app. Share this computer's address with the people who should watch, and send them to Jellyfin first.")}>
      <Card>
        <Table>
          <THead><TR><TH className="pl-4">{t("App")}</TH><TH>{t("User")}</TH><TH>{t("Password")}</TH></TR></THead>
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
                </> : <TD className="text-muted-foreground"><StatusBadge>{t("your own account")}</StatusBadge></TD>}
              </TR>
            ))}
          </TBody>
        </Table>
        {!list.length && <Empty>{t("No accounts to show yet.")}</Empty>}
      </Card>
    </Page>
  )
}

// ---------- Main channel ----------

type BotCheckResult = { ok: boolean; fixed: number; error?: string; bots: { id: number; name: string; state: string; detail?: string }[] }

// Asks Telegram what each bot can do in one channel (id 0 is the main channel). "Fix" adds the
// bots that are missing, with the signed-in account.
function BotCheck({ id, compact }: { id: number; compact?: boolean }) {
  const [res, setRes] = useState<BotCheckResult | null>(null)
  const { busy, run } = useBusy()
  const refresh = useRefreshState()
  const check = (fix: boolean) => void run(fix ? "fix" : "check", async () => {
    setRes(await api<BotCheckResult>("/panel/mirrors/check", { id, fix }))
    if (fix) await refresh()
  })
  const label = (state: string) => state === "ok" ? t("ok") : state === "no_post" ? t("may not post") : state === "offline" ? t("not connected") : t("not an admin")
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" disabled={!!busy} onClick={() => check(false)}>{busy === "check" ? t("Checking…") : t("Check bots in channel")}</Button>
        {res && !res.ok && <Button size="sm" disabled={!!busy} onClick={() => check(true)}>{busy === "fix" ? t("Adding…") : t("Add the missing bots")}</Button>}
      </div>
      {res && <div className={compact ? "space-y-1 text-xs" : "space-y-1 text-sm"}>
        {res.bots.map((b) => (
          <div key={b.id} className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{b.name || t("bot {{id}}", { id: b.id })}</span>
            <StatusBadge tone={b.state === "ok" ? "ok" : b.state === "offline" ? "warn" : "bad"}>{label(b.state)}</StatusBadge>
            {b.state !== "ok" && b.detail && <span className="text-muted-foreground">{b.detail}</span>}
          </div>
        ))}
        {res.fixed > 0 && <p className="text-muted-foreground">{t("{{count}} bots added.", { count: res.fixed })}</p>}
        {res.error && <p className="text-destructive">{res.error}</p>}
      </div>}
    </div>
  )
}

export function Channel() {
  const { data: s } = usePanel()
  const refresh = useRefreshState()
  const [editing, setEditing] = useState(false)
  const [channel, setChannel] = useState("")
  const { busy, run } = useBusy()
  if (!s) return null
  const search = s.telegram_search !== false
  return (
    <Page title={t("Main channel")} text={t("The channel that holds the library. Replacing it is refused once files are indexed, because the library would point at the wrong messages.")}>
      <Section title={t("Library channel")} actions={!editing && <Button size="sm" variant="outline" onClick={() => setEditing(true)}>{t("Replace")}</Button>}>
        <ChannelName name={s.channel_title} id={s.channel_id} className="text-sm font-medium" />
        <BotCheck id={0} />
        {editing && <div className="space-y-2">
          <FieldLabel htmlFor="main-channel">{t("New channel")}</FieldLabel>
          <Input id="main-channel" value={channel} placeholder={t("https://t.me/c/1234567890/5 or -100…")} onChange={(e) => setChannel(e.target.value)} />
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>{t("Cancel")}</Button>
            <Button size="sm" disabled={!channel.trim() || busy === "save"} onClick={() => void run("save", async () => {
              await api("/panel/settings", { channel })
              toast(t("Saved. Restart to use the new channel."))
              setEditing(false); setChannel(""); await refresh()
            })}>{t("Save")}</Button>
          </div>
        </div>}
      </Section>
      <Section>
        <Row title={t("Telegram search")} text={t("Look up a requested title on Telegram and copy a file that is found.")}>
          <SwitchBox label={t("Telegram search")} checked={search} onCheckedChange={(on) => void api("/panel/search", { on }).then(refresh).catch(fail)} />
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
        <Checkbox key={id} label={t(label)} disabled={disabled} checked={value.includes(id)} onCheckedChange={(on) => onChange(on ? [...value, id] : value.filter((k) => k !== id))} />
      ))}
    </div>
  )
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
  const [newBot, setNewBot] = useState(false)
  const [existing, setExisting] = useState("")
  const [exKinds, setExKinds] = useState<string[]>(["movie", "series"])
  const { busy, run } = useBusy()
  const running = !!job.data?.running
  useEffect(() => { if (mine && job.data && !job.data.running) void refresh() }, [mine, job.data?.running])
  if (!s) return null
  const live = s.bots.filter((b) => b.connected)
  const badUser = pub && user.trim() !== "" && !usernameOk(user.trim().replace(/^@/, ""))
  return (
    <Page title={t("Mirrors")} text={t("Each mirror is another channel. Choose which parts are copied there, The bots take turns posting them.")}>
      <Section title={t("Create mirror")} text={t("Makes a new channel with your Telegram account, makes every bot an admin, and starts copying. Nothing to do in Telegram.")}
        actions={!open && <Button size="sm" onClick={() => setOpen(true)}>{t("Create mirror")}</Button>}>
        {open && <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5 sm:col-span-2">
            <FieldLabel htmlFor="m-title">{t("Channel name")}</FieldLabel>
            <Input id="m-title" value={title} placeholder={t("Films")} onChange={(e) => setTitle(e.target.value)} />
          </div>
          <div className="sm:col-span-2"><Row title={t("Public")} text={pub ? t("Anyone with the link can join. A random username is chosen unless you type one.") : t("Only people you invite can join.")}><SwitchBox label={t("Public")} checked={pub} onCheckedChange={setPub} /></Row></div>
          {pub && <div className="space-y-1.5 sm:col-span-2">
            <FieldLabel htmlFor="m-user">{t("Username, optional")}</FieldLabel>
            <Input id="m-user" value={user} placeholder={t("Leave empty for a random one")} onChange={(e) => setUser(e.target.value)} aria-invalid={badUser} />
            {badUser && <p className="text-xs text-destructive">{t("Use 5 to 32 letters, digits or _, starting with a letter.")}</p>}
          </div>}
          <div className="space-y-1.5 sm:col-span-2"><FieldLabel>{t("Parts to copy")}</FieldLabel><KindPicks value={pick} onChange={setPick} /></div>
          <div className="sm:col-span-2"><Row title={t("Create a new bot too")} text={t("Every connected bot is made an admin of the mirror. A new bot joins the pool after a restart.")}><SwitchBox label={t("Create a new bot too")} checked={newBot || live.length === 0} onCheckedChange={setNewBot} /></Row></div>
          <div className="flex justify-end gap-2 sm:col-span-2">
            <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>{t("Cancel")}</Button>
            <Button size="sm" disabled={running || !title.trim() || !pick.length || badUser || busy === "create"} onClick={() => void run("create", async () => {
              await api("/panel/mirrors/create", { title: title.trim(), public: pub, username: user.trim().replace(/^@/, ""), kinds: pick, bot: newBot || live.length === 0 ? "new" : "" })
              setMine(true); setOpen(false); setTitle(""); setUser("")
              await job.refetch()
            })}>{running ? t("Working…") : t("Create")}</Button>
          </div>
        </div>}
        {(mine || running) && <>
          <ProgressSteps lines={job.data?.progress ?? []} />
          {job.data?.error && <Alert tone="bad">{job.data.error}</Alert>}
          {job.data?.result?.link && <CopyField label={t("Public link")} value={job.data.result.link} />}
        </>}
      </Section>

      <Card>
        <Table>
          <THead><TR><TH className="pl-4">{t("Channel")}</TH><TH>{t("Parts")}</TH><TH className="pr-4 text-right">{t("Actions")}</TH></TR></THead>
          <TBody>
            {(s.mirrors ?? []).map((m) => <MirrorRow key={m.id} m={m} bots={s.bots} onDone={refresh} />)}
          </TBody>
        </Table>
        {!s.mirrors?.length && <Empty>{t("No mirror yet.")}</Empty>}
      </Card>

      <Section title={t("Add existing channel")} text={t("A channel you already own. Every bot is made an admin there with your account, so nothing has to be done by hand.")}>
        <Input value={existing} placeholder={t("https://t.me/c/1234567890/5 or @films")} onChange={(e) => setExisting(e.target.value)} />
        <KindPicks value={exKinds} onChange={setExKinds} />
        <div className="flex justify-end">
          <Button size="sm" disabled={!existing.trim() || !live.length || !exKinds.length || busy === "existing"} onClick={() => void run("existing", async () => {
            await api("/panel/mirrors/admin", { channel: existing.trim() })
            await api("/panel/mirrors/add", { channel: existing.trim(), kinds: exKinds })
            setExisting(""); toast(t("Mirror added")); await refresh()
          })}>{t("Add channel")}</Button>
        </div>
      </Section>
    </Page>
  )
}

function MirrorRow({ m, bots, onDone }: { m: NonNullable<PanelState["mirrors"]>[number]; bots: PanelState["bots"]; onDone: () => Promise<unknown> }) {
  const { busy, run } = useBusy()
  const botLive = bots.some((b) => b.connected)
  // Every change is saved at once and the row redraws from the server, so it never shows stale values.
  const save = (k: string[]) => void run("save", async () => {
    await api("/panel/mirrors/save", { id: m.id, kinds: k })
    toast(t("Saved")); await onDone()
  })
  return (
    <TR>
      <TD className="pl-4">
        <ChannelName name={m.title} id={m.channel_id} className="font-medium" />
        <div className="flex flex-wrap items-center gap-1.5 pt-0.5 text-xs text-muted-foreground">
          {m.username ? <a className="underline-offset-2 hover:underline" href={"https://t.me/" + m.username} target="_blank" rel="noreferrer">t.me/{m.username}</a> : <StatusBadge>{t("private")}</StatusBadge>}
          <span>{t("{{count}} copied", { count: m.sent })}</span>
          {!botLive && <StatusBadge tone="warn">{t("bot connects after restart")}</StatusBadge>}
          {m.error && botLive && <StatusBadge tone="bad">{t("problem")}</StatusBadge>}
        </div>
        {m.error && botLive && <div className="pt-0.5 text-xs text-destructive">{m.error}</div>}
        <div className="pt-1.5"><BotCheck id={m.id} compact /></div>
      </TD>
      <TD><KindPicks value={m.kinds} disabled={busy === "save"} onChange={(k) => { if (k.length) save(k) }} /></TD>
      <TD className="pr-4">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="outline" disabled={busy === "vis"} onClick={() => void run("vis", async () => {
            const ok = await confirmDialog(m.username ? t("Make this mirror private?") : t("Make this mirror public?"),
              m.username ? t("The link t.me/{{username}} stops working. People already in the channel stay.", { username: m.username }) : t("Anyone with the link can join. A random username is chosen."), m.username ? t("Make private") : t("Make public"), false)
            if (!ok) return
            await api("/panel/mirrors/visibility", { id: m.id, public: !m.username })
            toast(m.username ? t("Now private") : t("Now public")); await onDone()
          })}>{m.username ? t("Make private") : t("Make public")}</Button>
          <Button size="sm" variant="destructive" disabled={busy === "rm"} onClick={() => void run("rm", async () => {
            const ok = await confirmDialog(t("Remove this mirror?"), t("Copying stops. The channel and what is already in it stay in Telegram."), t("Remove"))
            if (!ok) return
            await api("/panel/mirrors/remove", { id: m.id }); await onDone()
          })}>{t("Remove")}</Button>
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
  if (!f) return <Page title={t("Shared library")} text={t("The shared library is not available in this run.")}><span /></Page>
  async function act(action: string, body: unknown) {
    const out = await api<{ message?: string; restart?: boolean }>("/panel/forum/" + action, body)
    toast(out.message || (out.restart ? t("Saved. Restart to use the shared library.") : t("Saved")))
    await refresh()
  }
  const status = f.error ? t("The shared library is off: {{error}}", { error: f.error }) : f.fingerprint ? t("Running.") : f.group ? t("Restart to start it.") : ""
  const others = (f.trusted ?? []).filter((r) => !r.own)
  return (
    <Page title={t("Shared library")} text={t("Several installations can use this one channel. A new file is posted again with a signed line, so the others can trust it.")}>
      {status && <Alert tone={f.error ? "bad" : f.fingerprint ? "info" : "warn"} action={<Button size="sm" variant="outline" disabled={busy === "recheck"} onClick={() => void run("recheck", () => act("recheck", {}))}><RefreshCw className="size-3.5" />{t("Check again")}</Button>}>{status}</Alert>}
      <Section>
        <Row title={t("Repost new files with a signed caption")}><SwitchBox label={t("Shared library")} checked={!!f.group} onCheckedChange={(on) => void act("group", { on }).catch(fail)} /></Row>
        <Row title={t("Delete inbox files after they reach Ready")}><SwitchBox label={t("Delete originals")} checked={!!f.delete_originals} onCheckedChange={(on) => void act("originals", { on }).catch(fail)} /></Row>
        {f.fingerprint && <CopyField label={t("This installation")} value={f.fingerprint} />}
      </Section>
      <Section title={t("Trusted installations")} text={t("A key is believed once its posts have been checked against their files. Add one by hand by looking up the bot of the other installation.")}>
        {others.length ? (
          <Table>
            <THead><TR><TH>{t("Key")}</TH><TH>{t("Checked")}</TH><TH /></TR></THead>
            <TBody>{others.map((r) => (
              <TR key={r.fingerprint}>
                <TD><code className="text-xs">{r.fingerprint}</code>{r.manual && <div className="text-xs text-muted-foreground">{t("trusted by hand")}</div>}</TD>
                <TD><StatusBadge tone="ok">{t("{{count}} right", { count: r.verified ?? 0 })}</StatusBadge> {!!r.wrong && <StatusBadge tone="bad">{t("{{count}} wrong", { count: r.wrong })}</StatusBadge>}</TD>
                <TD className="text-right">{r.manual && <Button size="sm" variant="destructive" onClick={() => void run("rm", async () => {
                  if (await confirmDialog(t("Stop trusting this key?"), t("Posts signed with it are no longer believed."), t("Remove"))) await act("trust_remove", { fingerprint: r.fingerprint })
                })}>{t("Remove")}</Button>}</TD>
              </TR>
            ))}</TBody>
          </Table>
        ) : <Empty>{t("No other installation has posted yet.")}</Empty>}
        <div className="flex gap-2">
          <Input value={name} placeholder="@librarybot" onChange={(e) => { setName(e.target.value); setFound(null) }} />
          <Button size="sm" variant="outline" disabled={!name.trim() || busy === "lookup"} onClick={() => void run("lookup", async () => {
            const out = await api<{ fingerprint: string }>("/panel/forum/trust_lookup", { username: name.trim() })
            setFound({ user: name.trim(), fingerprint: out.fingerprint })
          })}>{t("Look up")}</Button>
        </div>
        {found && (
          <Alert action={<Button size="sm" disabled={busy === "trust"} onClick={() => void run("trust", async () => { await act("trust_add", { username: found.user }); setFound(null); setName("") })}>{t("Trust this key")}</Button>}>
            <>{t("Fingerprint")} <code>{found.fingerprint}</code>. {t("Trust it only if it matches what the other installation shows.")}</>
          </Alert>
        )}
      </Section>
    </Page>
  )
}

// ---------- Home videos ----------

type HomeView = { channel?: number; title?: string; problem?: string; folders?: { path: string; files?: number; error?: string }[]; files?: { name: string; kind?: string; status: string; note?: string }[] }

const homeTone = (st: string): Tone => /error|fail|problem/i.test(st) ? "bad" : /sent|synced|done|in telegram/i.test(st) ? "ok" : "warn"

export function HomeVideos() {
  const q = useQuery({ queryKey: ["home"], queryFn: () => api<HomeView>("/panel/home"), refetchInterval: () => (pollingPaused() ? false : 8000) })
  const h = q.data
  const channel = useDirty("")
  const [folder, setFolder] = useState("")
  const { busy, run } = useBusy()
  if (!h) return <Page title={t("Home videos")} text={t("Personal videos and photos.")}>{q.isError && <Alert tone="bad">{(q.error as Error).message}</Alert>}</Page>
  const files = h.files ?? []
  return (
    <Page title={t("Home videos")} text={t("Personal videos and photos. They get their own Telegram channel, and a Jellyfin library called Home Videos and Photos.")}>
      {h.problem && <Alert tone="bad">{h.problem}</Alert>}
      <Section title={t("Channel")} text={t("A private channel only for these files. Saving it needs a restart.")}>
        {!!h.channel && <ChannelName name={h.title} id={h.channel} className="text-sm font-medium" />}
        <div className="flex gap-2">
          <Input value={channel.value} onChange={(e) => channel.set(e.target.value)} placeholder={t("https://t.me/c/1234567890/5 or -100…")} />
          <Button size="sm" disabled={!channel.dirty || busy === "ch"} onClick={() => void run("ch", async () => { await api("/panel/home/channel", { channel: channel.value }); channel.saved(); toast(t("Saved. Restart to use it.")) })}>{t("Save")}</Button>
        </div>
      </Section>
      <Section title={t("Folders")} actions={<Button size="sm" variant="outline" disabled={busy === "sync"} onClick={() => void run("sync", async () => { const out = await api<{ queued?: number }>("/panel/home/sync", {}); toast(out.queued ? t("Queued {{count}}", { count: plural(out.queued, "file") }) : t("Nothing new to send")) })}>{t("Sync into the channel")}</Button>}>
        {(h.folders ?? []).map((f) => (
          <div key={f.path} className="flex items-center justify-between gap-2">
            <div className="min-w-0"><code className="block truncate text-sm">{f.path}</code>{f.error ? <div className="text-xs text-destructive">{f.error}</div> : <div className="text-xs text-muted-foreground">{plural(f.files ?? 0, "file")}</div>}</div>
            <Button size="sm" variant="ghost" onClick={() => void run("rm", async () => {
              if (!(await confirmDialog(t("Remove this folder?"), t("Its files on this computer are not touched."), t("Remove")))) return
              await api("/panel/home/folder", { path: f.path, remove: true }); await q.refetch()
            })}>{t("Remove")}</Button>
          </div>
        ))}
        {!h.folders?.length && <Empty>{t("No folder yet.")}</Empty>}
        <div className="flex gap-2">
          <Input value={folder} placeholder="D:\Home\Videos" onChange={(e) => setFolder(e.target.value)} />
          <Button size="sm" disabled={!folder.trim() || busy === "add"} onClick={() => void run("add", async () => { await api("/panel/home/folder", { path: folder.trim() }); setFolder(""); await q.refetch() })}>{t("Add folder")}</Button>
        </div>
      </Section>
      {!!files.length && <Card>
        <Table>
          <THead><TR><TH className="pl-4">{t("File")}</TH><TH className="pr-4 text-right">{t("Status")}</TH></TR></THead>
          <TBody>{files.slice(0, 80).map((f, i) => (
            <TR key={f.name + i}><TD className="pl-4"><div className="truncate">{f.name}</div>{f.note && <div className="text-xs text-muted-foreground">{f.note}</div>}</TD><TD className="pr-4 text-right"><StatusBadge tone={homeTone(f.status)}>{f.status}</StatusBadge></TD></TR>
          ))}</TBody>
        </Table>
        {files.length > 80 && <Empty>{t("And {{count}} more.", { count: files.length - 80 })}</Empty>}
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
    <Page title={t("Subtitles")} text={t("Pick every language to keep. The first one is preferred; drag or use the arrows to change the order. Leave none to skip subtitles.")}>
      <Section>
        <MultiSelect value={draft.value} onChange={draft.set} />
        <div className="flex justify-end"><Button size="sm" disabled={!draft.dirty || busy === "save"} onClick={() => void run("save", async () => {
          await api("/panel/settings", { language: draft.value.join(",") })
          draft.saved(); toast(t("Saved. Restart to use it.")); await refresh()
        })}>{t("Save")}</Button></div>
      </Section>
    </Page>
  )
}

// ---------- Auto-add filter ----------

export function AutoFilter() {
  return (
    <Page title={t("Auto-add filter")} text={t("Choose which trending titles are added to the library by themselves.")}>
      <Section><AutoFilterForm path="/panel/auto-filter" needsChange /></Section>
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
    <Page title={t("Telegram account")} text={t("Sign in first. Channels and the other tools open once an account is signed in. The account must be an admin of the library channel with permission to post.")}>
      <Card>
        <div className="flex items-center justify-between gap-2 border-b px-4 py-3">
          <h2 className="font-medium">{t("Accounts")}</h2>
          <Button size="sm" onClick={() => setOpen(true)}>{t("Add account")}</Button>
        </div>
        <Table>
          <TBody>{list.map((ses) => <SessionRow key={ses.user_id} ses={ses} onDone={refresh} />)}</TBody>
        </Table>
        {!list.length && <Empty>{t("No Telegram account is signed in.")}</Empty>}
      </Card>
      <p className="text-sm text-muted-foreground">{t("A file larger than the account can send is refused, not split. Sign-in works from this computer or over Tailscale only. The session is stored in data/users and is not encrypted, so keep that folder like a password.")}</p>
      <TelegramLoginDialog open={open} onOpenChange={setOpen} onDone={() => void refresh()} />
    </Page>
  )
}

function SessionRow({ ses, onDone }: { ses: Session; onDone: () => Promise<unknown> }) {
  const { busy, run } = useBusy()
  const name = ses.username ? "@" + ses.username : (ses.phone || t("user {{id}}", { id: ses.user_id }))
  const cap = Math.round((ses.max_bytes || 0) / 1e6)
  return (
    <TR>
      <TD className="pl-4">
        <div className="font-medium">{name}</div>
        <div className="flex flex-wrap items-center gap-1 pt-1">
          <StatusBadge tone={ses.connected ? "ok" : "warn"}>{ses.connected ? t("connected") : t("not connected")}</StatusBadge>
          <StatusBadge ok={!!ses.admin}>{ses.admin ? t("admin") : t("not an admin")}</StatusBadge>
          <StatusBadge>{ses.premium ? t("Premium") : t("Standard")}</StatusBadge>
          {cap > 0 && <span className="text-xs text-muted-foreground">{t("{{cap}} MB limit", { cap })}</span>}
          {ses.error && <StatusBadge tone="bad">{ses.error}</StatusBadge>}
        </div>
        {ses.busy && ses.total ? <div className="max-w-sm space-y-1 pt-2"><Progress value={((ses.progress || 0) / ses.total) * 100} /><div className="text-xs text-muted-foreground">{t("Uploading {{done}} of {{total}}", { done: bytes(ses.progress || 0), total: bytes(ses.total) })}</div></div> : null}
      </TD>
      <TD className="pr-4 text-right">
        <Button size="sm" variant="ghost" disabled={busy === "out"} onClick={() => void run("out", async () => {
          if (!(await confirmDialog(t("Sign out {{name}}?", { name }), t("The session on this computer is ended. Tools that need an account stop until one is signed in again."), t("Sign out")))) return
          await api("/panel/accounts/logout", { user_id: ses.user_id }); toast(t("Signed out")); await onDone()
        })}>{t("Sign out")}</Button>
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
    <Page title={t("Bots")} text={t("A bot is an account that reads the videos. The token is its password. This program never shows the full token again.")}>
      <Card>
        <Table>
          <THead><TR><TH className="pl-4">{t("Bot")}</TH><TH>{t("Status")}</TH><TH /></TR></THead>
          <TBody>{s.bots.map((b) => (
            <TR key={b.id}>
              <TD className="pl-4"><div className="font-medium">{b.name}</div><div className="text-xs text-muted-foreground">{t("token …{{hint}}", { hint: b.hint })}</div></TD>
              <TD>
                <div className="flex flex-wrap gap-1">
                  {!b.connected ? <StatusBadge tone="warn">{t("connects after restart")}</StatusBadge>
                    : b.ok && b.admin ? <StatusBadge tone="ok">{t("admin")}</StatusBadge>
                      : <StatusBadge tone="bad">{b.error || t("not an admin")}</StatusBadge>}
                </div>
              </TD>
              <TD className="pr-4 text-right">
                <Button size="sm" variant="ghost" disabled={s.bots.length < 2} title={s.bots.length < 2 ? t("Keep at least one bot") : undefined} onClick={() => void run("rm" + b.id, async () => {
                  if (!(await confirmDialog(t("Remove {{name}}?", { name: b.name }), t("Its token is deleted from the settings. Mirrors it posts to need another bot."), t("Remove")))) return
                  await api("/panel/bots", { remove: b.id }); await refresh()
                })}>{t("Remove")}</Button>
              </TD>
            </TR>
          ))}</TBody>
        </Table>
      </Card>

      <Section title={t("Create bots automatically")} text={t("Makes new bots with @BotFather using your Telegram account and adds them as admins of the main channel. They connect after a restart.")}>
        {!signed ? <SignInFirst /> : <>
          <div className="flex items-end gap-2">
            <div className="w-32 space-y-1.5"><FieldLabel>{t("How many")}</FieldLabel><Select label={t("How many bots")} value={count} onValueChange={setCount} options={[["1", t("1 bot")], ["2", t("2 bots")], ["3", t("3 bots")]]} /></div>
            <Button size="sm" className="h-9" disabled={running || busy === "create"} onClick={() => void run("create", async () => { await api("/panel/bots/create", { count: Number(count) }); setMine(true); await job.refetch() })}>{running ? t("Working…") : t("Create")}</Button>
          </div>
          {(mine || running) && <ProgressSteps lines={job.data?.progress ?? []} />}
          {mine && job.data?.error && <Alert tone="bad">{job.data.error}</Alert>}
          {mine && !!owned.length && <div className="space-y-2">
            <p className="text-sm">{t("This account already has the most bots Telegram allows. Pick bots to reuse; their old token stops working.")}</p>
            <div className="grid gap-2 sm:grid-cols-2">{owned.map((b) => <Checkbox key={b.username} label={"@" + b.username + (b.name ? " · " + b.name : "")} checked={picked.includes(b.username)} onCheckedChange={(on) => setPicked(on ? [...picked, b.username] : picked.filter((n) => n !== b.username))} />)}</div>
            <div className="flex justify-end"><Button size="sm" disabled={!picked.length || picked.length > 3 || running} onClick={() => void run("reuse", async () => { await api("/panel/bots/reuse", { usernames: picked }); setPicked([]); await job.refetch() })}>{t("Use the selected bots")}</Button></div>
          </div>}
        </>}
      </Section>

      <Section title={t("Add a bot")} text={t("Already have a token? Paste it here.")}>
        <Collapse title={t("How to create a bot by hand")}>
          <ol className="list-decimal space-y-1 pl-4">
            <li>{t("Open @BotFather in Telegram.")}</li>
            <li>{t("Send /newbot.")}</li>
            <li>{t("Choose a display name, then a username that ends in bot.")}</li>
            <li>{t("Make the bot an admin of the library channel.")}</li>
            <li>{t("Paste the token below. Do not post it in the channel.")}</li>
          </ol>
        </Collapse>
        <div className="flex gap-2">
          <Input value={token} placeholder="123456789:AAE..." onChange={(e) => setToken(e.target.value)} />
          <Button size="sm" className="h-9" disabled={!token.trim() || busy === "add"} onClick={() => void run("add", async () => { await api("/panel/bots", { add: token.trim() }); setToken(""); toast(t("Saved. Restart to connect it.")); await refresh() })}>{t("Add")}</Button>
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
    <Page title={t("Telegram API")} text={t("The API ID and API hash identify this program to Telegram. They are not the bot token.")}>
      <Section>
        {s.default_api && !show && <Row title={t("Default")} text={t("You're using the default. Replace it here if you want your own.")}><Button size="sm" variant="outline" onClick={() => setShow(true)}>{t("Replace")}</Button></Row>}
        {editing && <>
          <Collapse title={t("Where do I get these?")}>
            <p>{t("Telegram does not offer a public ID you can download or copy from someone else. Each person creates a pair, free, with their own phone number. This program speaks Telegram's own protocol, and that protocol refuses a connection without this pair.")}</p>
            <p className="pt-2">{t("Open my.telegram.org, then API development tools, and copy the numeric App api_id and the App api_hash.")}</p>
          </Collapse>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5"><FieldLabel htmlFor="api-id">{t("API ID")}</FieldLabel><Input id="api-id" inputMode="numeric" value={id} placeholder={s.api_id ? String(s.api_id) : "api_id"} onChange={(e) => setId(e.target.value)} /></div>
            <div className="space-y-1.5"><FieldLabel htmlFor="api-hash">{t("API hash")}</FieldLabel>
              <div className="relative">
                <Input id="api-hash" className="pr-9" type={reveal ? "text" : "password"} value={hash} placeholder={s.default_api ? "api_hash" : t("Leave empty to keep the saved hash")} onChange={(e) => setHash(e.target.value)} />
                <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground" aria-label={reveal ? t("Hide") : t("Show")} onClick={() => setReveal(!reveal)}>{reveal ? <EyeOff className="size-4" /> : <Eye className="size-4" />}</button>
              </div>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            {s.default_api && <Button size="sm" variant="ghost" onClick={() => setShow(false)}>{t("Cancel")}</Button>}
            <Button size="sm" disabled={busy === "save" || (!id.trim() && !hash.trim())} onClick={() => void run("save", async () => { await api("/panel/settings", { api_id: id, api_hash: hash }); setHash(""); toast(t("Saved. Restart to use it.")) })}>{t("Save")}</Button>
          </div>
        </>}
      </Section>
    </Page>
  )
}

// ---------- Direct files and News channels ----------

const lookbacks: [string, string][] = [["1", "1 day"], ["3", "3 days"], ["7", "1 week"], ["14", "2 weeks"], ["30", "1 month"], ["90", "3 months"], ["180", "6 months"], ["365", "1 year"], ["0", "No limit, all history"]]

type Rule = { enabled?: boolean; auto_add?: boolean; look_back_days?: number; every_minutes?: number; min_minutes?: number; min_mb?: number; max_gb?: number; max_per_day?: number; pause_seconds?: number; skip_words?: string; only_words?: string; need_title?: boolean }
type Src = { id: number; title?: string; username?: string; channel_id: number; enabled: boolean; strict?: boolean; no_forwards?: boolean; in_progress?: boolean; last_scan?: number; copied?: number; error?: string; look_back_days?: number }
type Recent = { name?: string; url?: string; source_id?: number; size?: number; updated?: number; state?: string; error?: string; detail?: string }
type SourcesView = {
  settings?: Rule; running?: boolean; today?: number; note?: string; last_pass?: number; accounts?: { total?: number; healthy?: number }
  sources?: Src[]; recent?: Recent[]
  news?: { settings?: Rule; running?: boolean; note?: string; channels?: Src[]; recent?: Recent[] }
}

export function Sources() { return <RulesPage kind="direct" /> }
export function News() { return <RulesPage kind="news" /> }

type DiscoverItem = { n: number; ref: string; score: number; members: number; title: string }
type DiscoverPage = { page: number; pages: number; items?: DiscoverItem[] }

function Discover({ news, onAdded }: { news: boolean; onAdded: () => void }) {
  const tag = news ? "news" : "direct-file"
  const [page, setPage] = useState(1)
  const [res, setRes] = useState<DiscoverPage | null>(null)
  const [err, setErr] = useState("")
  const { busy, run } = useBusy()
  const load = (p: number) => void run("discover", async () => {
    setErr("")
    try {
      const out = await api<DiscoverPage>("/panel/sources/discover", { tag, page: p })
      setPage(out.page || p)
      setRes(out)
    } catch (e) {
      setErr((e as Error).message)
    }
  })
  const items = res?.items ?? []
  return (
    <Section title={t("Suggested channels")}>
      <Row title={t("Channels other people have marked for this")} text={res && res.pages > 1 ? t("Page {{page}} of {{pages}}", { page: res.page, pages: res.pages }) : ""}>
        <Button size="sm" variant="outline" disabled={busy === "discover"} onClick={() => load(1)}>{t("Search")}</Button>
      </Row>
      {err && <Alert tone="bad">{err}</Alert>}
      {!!items.length && <Table>
        <TBody>{items.map((item) => (
          <TR key={item.n + item.ref}>
            <TD className="pl-4">
              <div className="font-medium">{item.title || item.ref}</div>
              <div className="text-xs text-muted-foreground">{item.ref} · {item.score} · {t("{{count}} members", { count: item.members })}</div>
            </TD>
            <TD className="pr-4">
              <div className="flex justify-end">
                <Button size="sm" variant="ghost" disabled={busy === "add" + item.ref || item.ref.startsWith("+")} onClick={() => void run("add" + item.ref, async () => {
                  await api("/panel/sources/discover_add", { ref: item.ref, tag })
                  toast(t("Added"))
                  onAdded()
                })}>{t("Add")}</Button>
              </div>
            </TD>
          </TR>
        ))}</TBody>
      </Table>}
      {res && !items.length && !err && <Empty>{t("Nothing on this page.")}</Empty>}
      {res && res.pages > 1 && <div className="flex justify-end gap-2">
        <Button size="sm" variant="outline" disabled={page <= 1 || busy === "discover"} onClick={() => load(page - 1)}>{t("Previous")}</Button>
        <Button size="sm" variant="outline" disabled={page >= res.pages || busy === "discover"} onClick={() => load(page + 1)}>{t("Next")}</Button>
      </div>}
    </Section>
  )
}

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
  const title = news ? t("News channels") : t("Direct files")
  if (!signed) return <Page title={title} text=""><SignInFirst /></Page>
  if (!v || !remote || !draft.value) return <Page title={title} text="">{q.isError && <Alert tone="bad">{(q.error as Error).message}</Alert>}</Page>
  const enabled = !!remote.enabled
  const running = !!(news ? v.news?.running : v.running)
  const note = news ? v.news?.note : v.note
  const list = (news ? v.news?.channels : v.sources) ?? []
  const recent = (news ? v.news?.recent : v.recent) ?? []
  const names: Record<number, string> = {}
  const tips: Record<number, string> = {}
  for (const s of list) {
    names[s.id] = s.title?.trim() || (s.username ? "@" + s.username : channelID(s.channel_id))
    tips[s.id] = channelID(s.channel_id)
  }
  const meta: string[] = []
  if (enabled) meta.push(running ? t("Checking now") : t("Next check within {{minutes}} minutes", { minutes: remote.every_minutes ?? 0 }))
  if (!news) meta.push(remote.max_per_day ? t("{{count}} copied today of {{max}}", { count: v.today ?? 0, max: remote.max_per_day }) : t("{{count}} copied today", { count: v.today ?? 0 }))
  if (!news && v.last_pass) meta.push(t("Last check {{when}}", { when: ago(v.last_pass) }))
  const act = (action: string) => "/panel/sources/" + (news ? "news_" + action : action)
  const toggleOn = async (on: boolean) => {
    await api(news ? "/panel/sources/news" : "/panel/sources/settings", { enabled: on })
    toast(on ? t("On. The first check starts now.") : t("Off. What was copied stays in the library."))
    await q.refetch()
  }
  const look = draft.value.look_back_days ?? 0
  const lookChoices = lookbacks.some(([k]) => k === String(look)) ? lookbacks : [...lookbacks, [String(look), look + " days"] as [string, string]]
  const lookOptions: [string, string][] = lookChoices.map(([k, lbl]) => [k, k === String(look) && !lookbacks.some(([bk]) => bk === String(look)) ? t("{{days}} days", { days: look }) : t(lbl)])
  const channelLooks: [string, string][] = [["-1", t("Same as the rule")], ...lookbacks.map(([k, lbl]): [string, string] => [k, t(lbl)])]
  return (
    <Page title={title} text={news ? t("These channels post a bot link instead of the film. Any free signed-in account opens the link and forwards the file into the library.") : t("Give the signed-in Telegram account other channels that post the film itself. New files are copied into the library.")}>
      <Section>
        <Row title={news ? (enabled ? t("Opening bot links is on") : t("Opening bot links is off")) : (enabled ? t("Copying from channels is on") : t("Copying from channels is off"))} text={meta.join(" · ")}>
          <SwitchBox label={t("Enabled")} checked={enabled} onCheckedChange={(on) => void toggleOn(on).catch(fail)} />
        </Row>
        {!news && !(v.accounts?.healthy) && <Alert tone="warn">{t("No signed-in account can read channels right now.")}</Alert>}
        {note && <Alert>{note}</Alert>}
      </Section>

      {enabled && <Section title={t("Auto-add channels")}>
        <Row title={t("Add useful channels by themselves")} text={t("Once a day one page is asked from the channel index. A channel is added when a recent post looks useful. The signed-in account only opens the channel to check.")}>
          <SwitchBox label={t("Auto-add")} checked={!!remote.auto_add} onCheckedChange={(on) => void run("auto", async () => {
            await api(act("settings"), { auto_add: on })
            toast(on ? t("Auto-add is on") : t("Auto-add is off"))
            await q.refetch()
          })} />
        </Row>
      </Section>}

      <Card>
        <div className="flex flex-wrap items-center gap-2 border-b px-4 py-3">
          <Input className="min-w-48 flex-1" value={value} placeholder={t("@channel or -100…")} onChange={(e) => setValue(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") (e.currentTarget.nextElementSibling as HTMLButtonElement | null)?.click() }} />
          <Button size="sm" className="h-9" disabled={!value.trim() || busy === "add"} onClick={() => void run("add", async () => {
            const res = await api<{ source?: { no_forwards?: boolean } }>(act("add"), { channel: value.trim() })
            setValue("")
            if (res.source?.no_forwards) toast.warning(t("Added, but its owner forbids copying files out of it."))
            else toast(t("Added"))
            await q.refetch()
            if (!enabled && await confirmDialog(news ? t("Turn news channels on?") : t("Turn direct file channels on?"), t("The channel is saved. Nothing is copied until this is on."), t("Turn on"), false)) await toggleOn(true)
          })}>{t("Add channel")}</Button>
          {!news && <>
            <Button size="sm" variant="outline" className="h-9" onClick={() => void run("check", async () => { await api("/panel/sources/check", {}); toast(t("Checking")); setTimeout(() => void q.refetch(), 1200) })}>{t("Check now")}</Button>
            <Button size="sm" variant="outline" className="h-9" onClick={() => void run("rescan", async () => { await api("/panel/sources/rescan", {}); toast(t("Every channel will be looked at again")); setTimeout(() => void q.refetch(), 1200) })}>{t("Look again")}</Button>
          </>}
        </div>
        <Table>
          <TBody>{list.map((src) => {
            const [label, tone] = srcStatus(src, running)
            return (
              <TR key={src.id}>
                <TD className="pl-4">
                  <ChannelName name={src.title || (src.username ? "@" + src.username : "")} id={src.channel_id} className="font-medium" />
                  <div className="flex flex-wrap items-center gap-1.5 pt-0.5 text-xs text-muted-foreground">
                    {src.title && src.username ? <span>@{src.username}</span> : null}
                    <StatusBadge tone={tone}>{t(label)}</StatusBadge>
                    <span>{news ? t("{{count}} forwarded", { count: src.copied ?? 0 }) : t("{{count}} copied", { count: src.copied ?? 0 })}</span>
                    {src.last_scan ? <span>{t("checked {{when}}", { when: ago(src.last_scan) })}</span> : null}
                  </div>
                  {src.error && <div className="pt-0.5 text-xs text-destructive">{src.error}</div>}
                </TD>
                <TD className="pr-4">
                  <div className="flex justify-end gap-1">
                    <Select size="sm" className="w-40" label={t("Look back")} value={String(src.look_back_days ?? -1)} onValueChange={(x) => void run("lb" + src.id, async () => { await api(act("lookback"), { id: src.id, days: Number(x) }); await q.refetch() })} options={channelLooks} />
                    {!src.no_forwards && <Button size="sm" variant="ghost" onClick={() => void run("p" + src.id, async () => { await api(act("pause"), { id: src.id, enabled: !src.enabled }); await q.refetch() })}>{src.enabled ? t("Pause") : t("Resume")}</Button>}
                    {!news && <Button size="sm" variant="ghost" title={t("Strict keeps only files whose name shows a year or an episode number. Use it for a channel that posts everything.")} onClick={() => void run("s" + src.id, async () => { await api("/panel/sources/strict", { id: src.id, strict: !src.strict }); await q.refetch() })}>{src.strict ? t("Strict: on") : t("Strict: off")}</Button>}
                    <Button size="sm" variant="ghost" className="text-destructive" onClick={() => void run("r" + src.id, async () => {
                      if (!(await confirmDialog(t("Remove this channel?"), t("Nothing more is copied from it. What is already in the library stays."), t("Remove")))) return
                      await api(act("remove"), { id: src.id }); toast(t("Channel removed")); await q.refetch()
                    })}>{t("Remove")}</Button>
                  </div>
                </TD>
              </TR>
            )
          })}</TBody>
        </Table>
        {!list.length && <Empty>{news ? t("No news channel yet.") : t("No channel yet. Add one above.")}</Empty>}
      </Card>

      <Discover news={news} onAdded={() => q.refetch()} />

      <Section title={t("What to copy")}>
        <Collapse title={t("How do I get a channel id?")}>
          <p>{t("A public channel is @name. For a private one, forward a message to @id_bot or @getidsbot. An invite link does not work.")}</p>
        </Collapse>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <div className="space-y-1.5"><FieldLabel>{t("Default look back")}</FieldLabel><Select label={t("Default look back")} value={String(draft.value.look_back_days ?? 0)} onValueChange={(x) => draft.set({ ...draft.value!, look_back_days: Number(x) })} options={lookOptions} /></div>
          <Num label={t("Check every, minutes")} k="every_minutes" draft={draft} />
          <Num label={t("Shortest video, minutes")} k="min_minutes" draft={draft} />
          <Num label={t("Smallest file, MB")} k="min_mb" draft={draft} />
          <Num label={t("Largest file, GB")} k="max_gb" draft={draft} />
          <Num label={t("Most videos per day")} k="max_per_day" draft={draft} />
          <Num label={t("Longest rest between bursts of 4-8 files, seconds")} k="pause_seconds" draft={draft} />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5"><FieldLabel>{t("Skip words")}</FieldLabel><Input value={draft.value.skip_words || ""} placeholder={t("trailer, sample")} onChange={(e) => draft.set({ ...draft.value!, skip_words: e.target.value })} /></div>
          <div className="space-y-1.5"><FieldLabel>{t("Only words")}</FieldLabel><Input value={draft.value.only_words || ""} onChange={(e) => draft.set({ ...draft.value!, only_words: e.target.value })} /></div>
        </div>
        <Row title={t("Only files that look like a film or an episode")}><SwitchBox label={t("Need a title")} checked={!!draft.value.need_title} onCheckedChange={(need_title) => draft.set({ ...draft.value!, need_title })} /></Row>
        <div className="flex justify-end gap-2">
          {draft.dirty && <Button size="sm" variant="ghost" onClick={() => draft.saved()}>{t("Discard")}</Button>}
          <Button size="sm" disabled={!draft.dirty || busy === "rules"} onClick={() => void run("rules", async () => {
            const { enabled: _skip, auto_add: _auto, ...rules } = draft.value!
            void _auto
            void _skip
            await api(act("settings"), rules); draft.saved(); toast(t("Rules saved")); await q.refetch()
          })}>{t("Save rules")}</Button>
        </div>
      </Section>

      <Card>
        <div className="border-b px-4 py-3"><h2 className="font-medium">{t("Recent")}</h2></div>
        <Table>
          <TBody>{recent.map((r, i) => {
            const [label, tone]: [string, Tone] = news ? (newsStates[r.state || ""] ?? [r.state || "waiting", "info"]) : (r.state === "copied" ? ["copied", "ok"] : ["skipped", "warn"])
            const displayLabel = news ? (newsStates[r.state || ""] ? t(label) : (r.state ?? t("waiting"))) : t(label)
            const extra = [r.source_id !== undefined ? names[r.source_id] : "", !news && r.size ? bytes(r.size) : "", r.updated ? ago(r.updated) : "", r.error || r.detail || ""].filter(Boolean).join(" · ")
            return (
              <TR key={i}>
                <TD className="pl-4"><div className="max-w-xl truncate">{news ? r.url : r.name}</div><div className="text-xs text-muted-foreground" title={r.source_id !== undefined ? tips[r.source_id] : undefined}>{extra}</div></TD>
                <TD className="pr-4 text-right"><StatusBadge tone={tone}>{displayLabel}</StatusBadge></TD>
              </TR>
            )
          })}</TBody>
        </Table>
        {!recent.length && <Empty>{news ? t("Nothing opened yet.") : t("Nothing copied yet.")}</Empty>}
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

function LocalProblem({ problem }: { problem: string }) {
  const { search } = useLocation()
  return <Alert tone="warn" action={<Button size="sm" variant="outline" asChild><Link to={{ pathname: "/telegram", search }}>{t("Add account")}</Link></Button>}>{problem}</Alert>
}

export function Local() {
  const q = useQuery({ queryKey: ["local"], queryFn: () => api<LocalView>("/panel/local"), refetchInterval: () => (pollingPaused() ? false : 4000) })
  const [path, setPath] = useState("")
  const [filter, setFilter] = useState("all")
  const { busy, run } = useBusy()
  const v = q.data
  if (!v) return <Page title={t("Local files")} text={t("Videos already on this computer.")}>{q.isError && <Alert tone="bad">{(q.error as Error).message}</Alert>}</Page>
  const files = (v.categories ?? []).flatMap((c) => (c.groups ?? []).flatMap((g) => g.files ?? []))
  const only = files.filter((f) => f.status === "local")
  const synced = files.filter((f) => f.status === "synced")
  const sum = (l: LocalFile[]) => l.reduce((n, f) => n + (f.size || 0), 0)
  const keep = (f: LocalFile) => filter === "all" || bucket(f.status) === filter
  const totals = v.totals ?? {}

  async function upload(ids: number[]) {
    const res = await api<LocalResult>("/panel/local/upload", { ids })
    if (res.done) toast(t("Queued {{files}} ({{size}}){{skipped}}", { files: plural(res.done, "file"), size: bytes(res.bytes ?? 0), skipped: res.skipped ? t(". {{count}} skipped, already sent or waiting.", { count: res.skipped }) : "" }))
    else toast(t("Nothing to queue. {{count}} were already sent or waiting.", { count: res.skipped ?? 0 }))
    await q.refetch()
  }
  async function remove(list: LocalFile[], label: string) {
    const what = list.length === 1 ? t("“{{label}}”", { label }) : plural(list.length, "file") + (label ? t(" of {{label}}", { label }) : "")
    const ok = await confirmDialog(t("Delete from this computer?"), t("This deletes {{what}} ({{size}}) from this computer for good. {{pronoun}} in the Telegram channel, and Jellyfin has read it from there. The channel copy stays.", { what, size: bytes(sum(list)), pronoun: list.length === 1 ? t("It is") : t("Each one is") }), list.length === 1 ? t("Delete the local copy") : t("Delete {{count}} local copies", { count: list.length }))
    if (!ok) return
    report(await api<LocalResult>("/panel/local/delete", { ids: list.map((f) => f.id) }))
    await q.refetch()
  }
  async function cancel(id: number) {
    await api("/panel/local/cancel", { ids: [id] }); toast(t("Taken out of the line")); await q.refetch()
  }
  const actions = { upload: (ids: number[]) => run("u", () => upload(ids)), remove: (l: LocalFile[], label: string) => run("d", () => remove(l, label)), cancel: (id: number) => run("c", () => cancel(id)) }

  return (
    <Page title={t("Local files")} text={t("Videos already on this computer, sorted into films and series. Nothing is uploaded or deleted until you press a button.")}>
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label={t("Only on this computer")} value={String(totals.local?.count ?? 0)} sub={totals.local?.count ? t("{{size}} to free once uploaded", { size: bytes(totals.local.bytes ?? 0) }) : t("Nothing is waiting")} />
        <Stat label={t("On the way")} value={String(totals.busy?.count ?? 0)} sub={totals.busy?.count ? bytes(totals.busy.bytes ?? 0) : t("Nothing is being sent")} />
        <Stat label={t("In Telegram")} value={String(totals.synced?.count ?? 0)} sub={totals.synced?.count ? t("{{size}} can be deleted from here", { size: bytes(totals.synced.bytes ?? 0) }) : t("Nothing yet")} />
        {!!totals.problem?.count && <Stat label={t("Needs a look")} value={String(totals.problem.count)} sub={t("Open the list to see why")} />}
      </div>
      {v.problem && <LocalProblem problem={v.problem} />}

      <Section title={t("Folders")} text={v.scanning ? t("Scanning…") : v.scanned ? t("Scanned at {{time}}", { time: new Date(v.scanned * 1000).toLocaleTimeString() }) : undefined}
        actions={<Button size="sm" variant="outline" disabled={busy === "scan" || v.scanning} onClick={() => void run("scan", async () => { await api("/panel/local/scan", {}); toast(t("Scanned")); await q.refetch() })}><RefreshCw className="size-3.5" />{t("Scan now")}</Button>}>
        {(v.folders ?? []).map((f) => (
          <div key={f.path} className="flex items-center justify-between gap-2">
            <div className="min-w-0"><code className="block truncate text-sm">{f.path}</code>
              {f.error ? <div className="text-xs text-destructive">{f.error}</div> : <div className="text-xs text-muted-foreground">{plural(f.files ?? 0, "video")} · {bytes(f.bytes ?? 0)}</div>}</div>
            <Button size="sm" variant="ghost" onClick={() => void run("rmf", async () => {
              if (!(await confirmDialog(t("Remove this folder?"), t("Its files are not touched. They just stop showing here."), t("Remove")))) return
              await api("/panel/local/folder", { remove: f.path }); toast(t("Folder removed. Its files were not touched.")); await q.refetch()
            })}>{t("Remove")}</Button>
          </div>
        ))}
        {!v.folders?.length && <Empty>{t("No folder yet. Add the folder that holds your films or series.")}</Empty>}
        <div className="flex gap-2">
          <Input value={path} placeholder="D:\Library\Movie" onChange={(e) => setPath(e.target.value)} />
          <Button size="sm" className="h-9" disabled={!path.trim() || busy === "addf"} onClick={() => void run("addf", async () => { await api("/panel/local/folder", { add: path.trim() }); setPath(""); toast(t("Folder added. Scanning it now.")); setTimeout(() => void q.refetch(), 1500) })}>{t("Add folder")}</Button>
        </div>
      </Section>

      <Section>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Toggles value={filter} onChange={setFilter} options={localChips.filter(([k]) => k !== "problem" || files.some((f) => f.status === "problem") || filter === "problem").map(([k, label]) => [k, t(label) + " " + (k === "all" ? files.length : files.filter((f) => bucket(f.status) === k).length)])} />
          <div className="flex flex-wrap gap-2">
            <Button size="sm" disabled={!only.length || !!v.problem || busy === "u"} onClick={() => void actions.upload(only.map((f) => f.id))}>
              {only.length ? t("Upload everything that is only here ({{count}}, {{size}})", { count: only.length, size: bytes(sum(only)) }) : t("Upload everything that is only here")}
            </Button>
            <Button size="sm" variant="destructive" disabled={!synced.length || busy === "d"} onClick={() => void run("d", async () => {
              const ok = await confirmDialog(t("Delete from this computer?"), t("This deletes {{count}} local copies ({{size}}) that are already in Telegram. The channel copies stay.", { count: synced.length, size: bytes(sum(synced)) }), t("Delete {{count}}", { count: synced.length }))
              if (!ok) return
              report(await api<LocalResult>("/panel/local/delete_synced", {})); await q.refetch()
            })}>{synced.length ? t("Delete every local copy that is in Telegram ({{count}})", { count: synced.length }) : t("Delete every local copy that is in Telegram")}</Button>
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
          {!files.filter(keep).length && <Empty>{v.folders?.length ? (filter === "all" ? t("No videos found in these folders yet.") : t("Nothing here.")) : t("Add a folder above to see its videos.")}</Empty>}
        </div>
      </Section>
    </Page>
  )
}

function report(res: LocalResult) {
  const errs = res.errors ?? []
  if (res.done) toast(t("Deleted {{files}} and freed {{size}}{{kept}}", { files: plural(res.done, "file"), size: bytes(res.bytes ?? 0), kept: errs.length ? t(". Kept {{count}}: {{detail}}", { count: errs.length, detail: errs.slice(0, 2).join(" ") }) : "" }))
  else toast.error(t("Nothing was deleted.{{detail}}", { detail: errs.length ? " " + errs.slice(0, 3).join(" ") : "" }))
}

type LocalActions = { upload: (ids: number[]) => unknown; remove: (l: LocalFile[], label: string) => unknown; cancel: (id: number) => unknown }

function GroupBlock({ cat, g, actions }: { cat: string; g: LocalGroup & { files: LocalFile[] }; actions: LocalActions }) {
  const name = g.title + (g.year ? " (" + g.year + ")" : "")
  const only = g.files.filter((f) => f.status === "local")
  const synced = g.files.filter((f) => f.status === "synced")
  return (
    <Disclosure level={2} title={name}
      meta={plural(g.files.length, cat === "shows" ? "episode" : "file") + " · " + bytes(g.bytes ?? g.files.reduce((n, f) => n + f.size, 0)) + (synced.length ? " · " + t("{{count}} in Telegram", { count: synced.length }) : "")}
      actions={<>
        {!!only.length && <Button size="sm" variant="outline" onClick={() => void actions.upload(only.map((f) => f.id))}>{t("Upload {{count}}", { count: only.length })}</Button>}
        {!!synced.length && <Button size="sm" variant="ghost" className="text-destructive" onClick={() => void actions.remove(synced, name)}>{t("Delete {{count}} local", { count: synced.length })}</Button>}
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
    case "local": side = <><StatusBadge>{t("Only here")}</StatusBadge>{btn(t("Upload"), () => actions.upload([f.id]))}</>; break
    case "waiting": side = <><StatusBadge tone="info">{t("Waiting")}</StatusBadge>{btn(t("Cancel"), () => actions.cancel(f.id))}</>; break
    case "uploading": side = <StatusBadge tone="info">{t("Uploading")}</StatusBadge>; break
    case "checking": side = <StatusBadge tone="info">{t("In Telegram, checking")}</StatusBadge>; break
    case "synced": side = <><StatusBadge tone="ok">{t("In Telegram")}</StatusBadge>{btn(t("Delete local copy"), () => actions.remove([f], f.name), true)}</>; break
    case "problem": side = <><StatusBadge tone="bad">{t("Needs a look")}</StatusBadge>{f.state === "mismatch" && btn(t("Upload again"), () => actions.upload([f.id]))}{btn(t("Cancel"), () => actions.cancel(f.id))}</>; break
  }
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 border-t py-2 first:border-t-0">
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm">{title}</div>
        <div className="truncate text-xs text-muted-foreground">{[f.rel, bytes(f.size)].filter(Boolean).join(" · ")}</div>
        {f.note && <div className={cn("text-xs", f.status === "problem" ? "text-destructive" : "text-muted-foreground")}>{f.note}</div>}
        {f.status === "uploading" && f.total ? <div className="max-w-sm space-y-1 pt-1"><Progress value={((f.progress ?? 0) / f.total) * 100} /><div className="text-xs text-muted-foreground">{t("{{done}} of {{total}}", { done: bytes(f.progress ?? 0), total: bytes(f.total) })}</div></div> : null}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">{side}</div>
    </div>
  )
}

// ---------- Upload pace ----------

type Pace = Record<string, number | string>

function num(pace: Pace | undefined, a: string, b: string) {
  return Number(pace?.[a] ?? pace?.[b] ?? 0)
}

export function UploadPace() {
  const { data: s } = usePanel()
  const d = (s?.pace ?? undefined) as Pace | undefined
  const pace = useDirty({
    mb_per_sec: num(d, "mb_per_sec", "MBPerSec"),
    rest_minutes: num(d, "rest_minutes", "RestMinutes"),
    long_every_gb: num(d, "long_every_gb", "LongEveryGB"),
    long_rest_minutes: num(d, "long_rest_minutes", "LongRestMinutes"),
    daily_gb: num(d, "daily_gb", "DailyGB"),
    hours: String(d?.hours ?? d?.Hours ?? ""),
  })
  const { busy, run } = useBusy()
  if (!d) return null
  const field = (label: string, k: keyof typeof pace.value, placeholder?: string) => (
    <div className="space-y-1.5"><FieldLabel>{label}</FieldLabel><Input value={String(pace.value[k])} placeholder={placeholder} inputMode={k === "hours" ? undefined : "decimal"}
      onChange={(e) => pace.set({ ...pace.value, [k]: k === "hours" ? e.target.value : Number(e.target.value) })} /></div>
  )
  return (
    <Page title={t("Upload pace")} text={t("How fast and when files go to Telegram, so the account is not flagged.")}>
      <Section title={t("Pace")}>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {field(t("Megabytes per second"), "mb_per_sec")}
          {field(t("Minutes between files"), "rest_minutes")}
          {field(t("Long rest after this many GB"), "long_every_gb")}
          {field(t("Long rest, minutes"), "long_rest_minutes")}
          {field(t("Daily GB per account"), "daily_gb")}
          {field(t("Hours"), "hours", "01-08")}
        </div>
        <div className="flex justify-end"><Button size="sm" disabled={!pace.dirty || busy === "pace"} onClick={() => void run("pace", async () => { await api("/panel/accounts/pace", pace.value); pace.saved(); toast(t("Pace saved")) })}>{t("Save pace")}</Button></div>
      </Section>
    </Page>
  )
}

// ---------- Away from home ----------

export function Away() {
  const { data: s } = usePanel()
  return (
    <Page title={t("Away from home")} text={t("At home, open an address tagged At home. Away from home, use Tailscale. It is a private network between your own devices. Nothing is published on the public internet.")}>
      <Section title={t("Set up Tailscale")}>
        <ol className="list-decimal space-y-1 pl-5 text-sm">
          <li>{t("Install Tailscale on this computer.")}</li>
          <li>{t("Install it on the phone or the TV.")}</li>
          <li>{t("Sign both devices in to the same Tailscale account.")}</li>
          <li>{t("Open the address tagged Away from home below.")}</li>
          <li>{t("Do not turn on Tailscale Funnel. Funnel would publish Jellyfin to the whole internet, and it is a poor fit for video. No router port has to be opened.")}</li>
        </ol>
      </Section>
      <Card>
        <Table><TBody>{(s?.addresses ?? []).map((a) => (
          <TR key={a.url}>
            <TD className="w-36 pl-4"><StatusBadge tone={a.away ? "info" : "ok"}>{a.away ? t("Away from home") : t("At home")}</StatusBadge></TD>
            <TD className="pr-4"><CopyField label="" value={a.url} /></TD>
          </TR>
        ))}</TBody></Table>
        {!s?.addresses?.length && <Empty>{t("No network address yet.")}</Empty>}
      </Card>
    </Page>
  )
}
