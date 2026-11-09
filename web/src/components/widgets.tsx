import { useEffect, useRef, useState, type ReactNode } from "react"
import { Check, ChevronDown, Copy, GripVertical, Loader2, X } from "lucide-react"
import { toast } from "sonner"
import { languageName, languages } from "@/lib/languages"
import { api, pausePolling } from "@/lib/api"
import { cn } from "@/lib/utils"
import { Button, Command, CommandEmpty, CommandInput, CommandItem, CommandList, DialogContent, DialogRoot, Input, PopoverContent, PopoverRoot, PopoverTrigger } from "@/components/ui"

export function CopyField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-2">
      <div className="min-w-0 flex-1">
        <div className="text-xs text-muted-foreground">{label}</div>
        <code className="block truncate text-sm">{value || "—"}</code>
      </div>
      <Button type="button" size="sm" variant="outline" onClick={() => { void navigator.clipboard.writeText(value); toast("Copied") }}><Copy className="size-3.5" />Copy</Button>
    </div>
  )
}

export type Tone = "ok" | "bad" | "warn" | "info"

// StatusBadge is a small pill. Give tone, or ok=true/false for the usual good/bad pair.
export function StatusBadge({ ok, tone, children }: { ok?: boolean; tone?: Tone; children: ReactNode }) {
  const t: Tone = tone ?? (ok === true ? "ok" : ok === false ? "bad" : "info")
  return <span className={cn("inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-full px-2 text-[11px] font-medium",
    t === "bad" && "bg-destructive/15 text-destructive",
    t === "ok" && "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
    t === "warn" && "bg-amber-500/15 text-amber-700 dark:text-amber-300",
    t === "info" && "bg-muted text-muted-foreground")}>{children}</span>
}

export function ProgressSteps({ lines }: { lines: { text: string; state: string }[] }) {
  if (!lines?.length) return null
  return (
    <ul className="space-y-1.5 rounded-md border bg-muted/40 p-3 text-sm">
      {lines.map((line, i) => (
        <li key={i} className={cn("flex items-start gap-2", line.state === "bad" && "text-destructive", line.state === "wait" && "text-muted-foreground")}>
          {line.state === "ok" ? <Check className="mt-0.5 size-4 shrink-0 text-emerald-600" /> : line.state === "bad" ? <X className="mt-0.5 size-4 shrink-0" /> : <Loader2 className="mt-0.5 size-4 shrink-0 animate-spin" />}
          <span>{line.text}</span>
        </li>
      ))}
    </ul>
  )
}

type ConfirmState = { title: string; text: string; button: string; danger: boolean; resolve: (v: boolean) => void } | null
let setConfirm: ((v: ConfirmState) => void) | null = null

export function confirmDialog(title: string, text: string, button: string, danger = true) {
  return new Promise<boolean>((resolve) => setConfirm?.({ title, text, button, danger, resolve }))
}

export function ConfirmHost() {
  const [state, setState] = useState<ConfirmState>(null)
  useEffect(() => { setConfirm = setState; return () => { setConfirm = null } }, [])
  useEffect(() => { pausePolling(!!state); return () => { if (state) pausePolling(false) } }, [state])
  return (
    <DialogRoot open={!!state} onOpenChange={(open) => { if (!open) { state?.resolve(false); setState(null) } }}>
      {state && (
        <DialogContent title={state.title}>
          <p className="text-sm text-muted-foreground">{state.text}</p>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => { state.resolve(false); setState(null) }}>Cancel</Button>
            <Button variant={state.danger ? "destructive" : "default"} size="sm" onClick={() => { state.resolve(true); setState(null) }}>{state.button}</Button>
          </div>
        </DialogContent>
      )}
    </DialogRoot>
  )
}

export function MultiSelect({ value, onChange }: { value: string[]; onChange: (next: string[]) => void }) {
  const [open, setOpen] = useState(false)
  const [drag, setDrag] = useState<string | null>(null)
  function toggle(code: string) {
    onChange(value.includes(code) ? value.filter((c) => c !== code) : [...value, code])
  }
  function move(code: string, dir: -1 | 1) {
    const i = value.indexOf(code)
    const j = i + dir
    if (i < 0 || j < 0 || j >= value.length) return
    const next = value.slice()
    const [item] = next.splice(i, 1)
    next.splice(j, 0, item!)
    onChange(next)
  }
  return (
    <div className="space-y-2">
      <PopoverRoot open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button type="button" variant="outline" className="h-9 w-full justify-between font-normal">
            <span className="truncate">{value.length ? value.map(languageName).join(", ") : "No subtitle languages"}</span>
            <ChevronDown className="size-4 opacity-60" />
          </Button>
        </PopoverTrigger>
        <PopoverContent>
          <Command>
            <CommandInput placeholder="Search languages" />
            <CommandList>
              <CommandEmpty>No language</CommandEmpty>
              {languages.map(([code, name]) => (
                <CommandItem key={code} value={name} onSelect={() => toggle(code)}>
                  <Check className={cn("size-3.5", value.includes(code) ? "opacity-100" : "opacity-0")} />
                  {name}
                </CommandItem>
              ))}
            </CommandList>
          </Command>
        </PopoverContent>
      </PopoverRoot>
      <div className="flex flex-wrap gap-1">
        {value.map((code, i) => (
          <span key={code} draggable onDragStart={() => setDrag(code)} onDragOver={(e) => e.preventDefault()} onDrop={() => {
            if (!drag || drag === code) return
            const next = value.filter((c) => c !== drag)
            next.splice(next.indexOf(code), 0, drag)
            onChange(next)
            setDrag(null)
          }} className="inline-flex h-7 items-center gap-1 rounded-full border bg-card px-2 text-xs">
            <GripVertical className="size-3 opacity-50" />
            {i === 0 ? "Preferred · " : ""}{languageName(code)}
            <button type="button" className="px-0.5" aria-label="Move up" onClick={() => move(code, -1)}>↑</button>
            <button type="button" className="px-0.5" aria-label="Move down" onClick={() => move(code, 1)}>↓</button>
            <button type="button" aria-label="Remove" onClick={() => toggle(code)}>×</button>
          </span>
        ))}
      </div>
    </div>
  )
}

type LoginStep = "phone" | "code" | "password"
const loginCopy = {
  phone: { title: "Your Telegram phone number", help: "Use the account that is an admin of the channel, with the country code. Telegram sends nothing until you press the button.", button: "Send code" },
  code: { title: "Enter the code", help: "Telegram sent a code to the Telegram app on that account, or by text message. It stays valid for a few minutes.", button: "Confirm code" },
  password: { title: "Two-step verification", help: "This account has a password. It goes to Telegram only and is not saved here.", button: "Sign in" },
}

export function LoginForm({ path, extra, onDone, resend }: { path: string; extra?: Record<string, string>; onDone: () => void; resend?: boolean }) {
  const [step, setStep] = useState<LoginStep>("phone")
  const [value, setValue] = useState("")
  const [note, setNote] = useState("")
  const [err, setErr] = useState("")
  const [busy, setBusy] = useState(false)
  const lock = useRef(false)
  const copy = loginCopy[step]
  async function next() {
    if (lock.current) return
    lock.current = true
    setBusy(true); setErr("")
    try {
      const body = step === "phone" ? { phone: value.trim(), ...extra } : step === "code" ? { code: value.trim() } : { password: value }
      const res = await api<{ step?: string; note?: string; warning?: string }>(path, body)
      if (res.note) setNote(res.note)
      if (res.step === "code" || res.step === "password") { setStep(res.step); setValue(""); if (res.warning) setErr(res.warning); return }
      onDone()
    } catch (e) { setErr(e instanceof Error ? e.message : "Something went wrong") }
    finally { lock.current = false; setBusy(false) }
  }
  return (
    <div className="space-y-3">
      <div>
        <h3 className="font-medium">{copy.title}</h3>
        <p className="text-sm text-muted-foreground">{note || copy.help}</p>
      </div>
      <Input disabled={busy} value={value} onChange={(e) => setValue(e.target.value)} type={step === "password" ? "password" : "text"} inputMode={step === "phone" ? "tel" : step === "code" ? "numeric" : undefined} autoComplete={step === "code" ? "one-time-code" : "off"} placeholder={step === "phone" ? "+15551212" : step === "code" ? "12345" : ""} onKeyDown={(e) => { if (e.key === "Enter") void next() }} />
      {err && <p className="text-sm text-destructive">{err}</p>}
      <div className="flex justify-end gap-2">
        {resend && step === "code" && <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={() => { if (lock.current) return; lock.current = true; setBusy(true); void api<{ note?: string }>(path, { resend: true }).then((r) => { if (r.note) setNote(r.note); toast("Asked Telegram to send it another way") }).catch((e) => setErr(e instanceof Error ? e.message : "Something went wrong")).finally(() => { lock.current = false; setBusy(false) }) }}>Send by text message</Button>}
        {step !== "phone" && <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={() => { if (lock.current) return; void api(path, { cancel: true }); setStep("phone"); setValue("") }}>Change number</Button>}
        <Button type="button" size="sm" loading={busy} onClick={() => void next()}>{copy.button}</Button>
      </div>
    </div>
  )
}

export function TelegramLoginDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; onDone: () => void }) {
  useEffect(() => { pausePolling(open); return () => { if (open) pausePolling(false) } }, [open])
  return (
    <DialogRoot open={open} onOpenChange={(v) => { if (!v) void api("/panel/downloads/login", { cancel: true }).catch(() => {}); onOpenChange(v) }}>
      {open && <DialogContent title="Add account"><LoginForm path="/panel/downloads/login" onDone={() => { onOpenChange(false); onDone() }} /></DialogContent>}
    </DialogRoot>
  )
}
