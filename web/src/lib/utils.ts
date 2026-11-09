import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"
import { t } from "@/lib/i18n"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function bytes(n: number) {
  if (!n) return "0 MB"
  if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
  return Math.round(n / 1e6) + " MB"
}

export function plural(n: number, word: string) {
  return n + " " + t(n === 1 ? word : word + "s")
}

// ago turns a unix time into a short "5 min ago". Zero means it never happened.
export function ago(unix?: number) {
  if (!unix) return t("not checked yet")
  const sec = Math.max(0, Math.round(Date.now() / 1000 - unix))
  if (sec < 90) return t("just now")
  if (sec < 5400) return t("{{n}} min ago", { n: Math.round(sec / 60) })
  if (sec < 172800) return t("{{n}} h ago", { n: Math.round(sec / 3600) })
  return t("{{n}} days ago", { n: Math.round(sec / 86400) })
}
