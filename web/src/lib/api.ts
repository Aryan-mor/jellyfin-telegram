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

export async function api<T = Record<string, unknown>>(path: string, body?: unknown): Promise<T> {
  const q = new URLSearchParams()
  const cut = path.indexOf("?")
  const base = cut === -1 ? path : path.slice(0, cut)
  if (cut !== -1) new URLSearchParams(path.slice(cut + 1)).forEach((v, k) => q.set(k, v))
  q.set("code", panelCode())
  const res = await fetch(base + "?" + q.toString(), body === undefined ? undefined : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
  const data = await res.json().catch(() => ({})) as { error?: string; restart?: boolean }
  if (!res.ok) throw new ApiError(data.error || "Something went wrong")
  if (data.restart) window.dispatchEvent(new Event("panel-restart"))
  return data as T
}
