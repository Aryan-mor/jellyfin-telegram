let pauses = 0

export function pausePolling(on: boolean) {
  pauses += on ? 1 : -1
  if (pauses < 0) pauses = 0
}

export function pollingPaused() {
  return pauses > 0 || document.hidden
}

declare global {
  interface Window {
    __panelCode?: string
  }
}

export function panelCode() {
  return new URLSearchParams(location.search).get("code") || window.__panelCode || ""
}

export class ApiError extends Error {
  constructor(message: string) {
    super(message)
    this.name = "ApiError"
  }
}

export async function api<T = Record<string, unknown>>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const q = new URLSearchParams()
  const cut = path.indexOf("?")
  const base = cut === -1 ? path : path.slice(0, cut)
  if (cut !== -1) new URLSearchParams(path.slice(cut + 1)).forEach((v, k) => q.set(k, v))
  q.set("code", panelCode())
  // A restart leaves the browser holding a dead connection. Without a timeout that request
  // never fails, later polls queue behind it, and only a refresh opens a live socket.
  const timed = body === undefined ? AbortSignal.timeout(8000) : undefined
  const signals = [signal, timed].filter((s): s is AbortSignal => !!s)
  const merged = signals.length <= 1 ? signals[0] : AbortSignal.any(signals)
  const res = await fetch(base + "?" + q.toString(), {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: merged,
    cache: "no-store",
  })
  const text = await res.text()
  let data: { error?: string; restart?: boolean } = {}
  if (text) {
    try {
      data = JSON.parse(text) as { error?: string; restart?: boolean }
    } catch {
      data = { error: text.trim() }
    }
  }
  if (!res.ok) {
    const message = data.error || "Something went wrong"
    // The access code is new on every start. The open page still has the old one, so the
    // ready poll is refused and the "getting ready" screen never leaves. Load the page again.
    if (message.includes("open /panel")) reloadForNewCode()
    throw new ApiError(message)
  }
  if (data.restart) window.dispatchEvent(new Event("panel-restart"))
  return data as T
}

function reloadForNewCode() {
  const key = "jftg-panel-reload"
  const prev = Number(sessionStorage.getItem(key) || 0)
  if (Date.now() - prev < 15000) return
  sessionStorage.setItem(key, String(Date.now()))
  location.reload()
}
