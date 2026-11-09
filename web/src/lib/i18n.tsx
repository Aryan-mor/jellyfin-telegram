import { useEffect, useState, type ReactNode } from "react"
import fa from "@/locales/fa.json"
import de from "@/locales/de.json"
import tr from "@/locales/tr.json"

export const uiLanguages = [
  { code: "en", native: "English", dir: "ltr" },
  { code: "fa", native: "فارسی", dir: "rtl" },
  { code: "de", native: "Deutsch", dir: "ltr" },
  { code: "tr", native: "Türkçe", dir: "ltr" },
] as const

export type UiCode = (typeof uiLanguages)[number]["code"]

const catalogs: Record<Exclude<UiCode, "en">, Record<string, string>> = { fa, de, tr }
const storageKey = "jftg-ui-lang"

const listeners = new Set<() => void>()
let current = readStored()
let adopted = false
let userChose = false

function isUi(code: string | null | undefined): code is UiCode {
  return uiLanguages.some((item) => item.code === code)
}

function readStored(): UiCode {
  try {
    const saved = localStorage.getItem(storageKey)
    if (isUi(saved)) return saved
  } catch {
    // private mode
  }
  const nav = (navigator.language || "en").toLowerCase()
  if (nav.startsWith("fa")) return "fa"
  if (nav.startsWith("de")) return "de"
  if (nav.startsWith("tr")) return "tr"
  return "en"
}

function applyDom(code: UiCode) {
  const item = uiLanguages.find((lang) => lang.code === code)!
  document.documentElement.lang = code
  document.documentElement.dir = item.dir
}

applyDom(current)

export function uiLang() {
  return current
}

export function t(message: string, vars?: Record<string, string | number>) {
  const table = current === "en" ? undefined : catalogs[current]
  let out = table?.[message] || message
  if (vars) {
    for (const [key, value] of Object.entries(vars)) out = out.replaceAll("{{" + key + "}}", String(value))
  }
  return out
}

function onPanel() {
  return location.pathname === "/panel" || location.pathname.startsWith("/panel/")
}

export function setUiLang(code: string, opts?: { remote?: boolean }) {
  if (!isUi(code)) return
  const changed = code !== current
  current = code
  try { localStorage.setItem(storageKey, code) } catch { /* ignore */ }
  applyDom(code)
  if (changed) listeners.forEach((fn) => fn())
  if (opts?.remote !== false) userChose = true
  if (opts?.remote !== false && onPanel()) {
    const q = new URLSearchParams(location.search).get("code") || window.__panelCode || ""
    void fetch("/panel/ui-language?code=" + encodeURIComponent(q), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ language: code }),
    }).catch(() => {})
  }
}

// adoptServerLanguage applies the language saved with this install, once.
export function adoptServerLanguage(code: string | undefined) {
  if (adopted || userChose || !isUi(code)) return
  adopted = true
  if (code !== current) setUiLang(code, { remote: false })
}

export function useUiLang() {
  const [lang, setLang] = useState(current)
  useEffect(() => {
    const fn = () => setLang(current)
    listeners.add(fn)
    return () => { listeners.delete(fn) }
  }, [])
  return lang
}

export function I18nProvider({ children }: { children: ReactNode }) {
  useUiLang()
  return children
}

export function UiLanguageSelect() {
  const lang = useUiLang()
  return (
    <label className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
      <span>{t("Language")}</span>
      <select
        className="h-8 max-w-36 rounded-md border bg-background px-2 text-xs text-foreground"
        value={lang}
        aria-label={t("Language")}
        onChange={(e) => setUiLang(e.target.value)}
      >
        {uiLanguages.map((item) => <option key={item.code} value={item.code}>{item.native}</option>)}
      </select>
    </label>
  )
}
