import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function bytes(n: number) {
  if (!n) return "0 MB"
  if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
  return Math.round(n / 1e6) + " MB"
}

export function plural(n: number, word: string) {
  return n + " " + word + (n === 1 ? "" : "s")
}

// ago turns a unix time into a short "5 min ago". Zero means it never happened.
export function ago(unix?: number) {
  if (!unix) return "not checked yet"
  const sec = Math.max(0, Math.round(Date.now() / 1000 - unix))
  if (sec < 90) return "just now"
  if (sec < 5400) return Math.round(sec / 60) + " min ago"
  if (sec < 172800) return Math.round(sec / 3600) + " h ago"
  return Math.round(sec / 86400) + " days ago"
}
