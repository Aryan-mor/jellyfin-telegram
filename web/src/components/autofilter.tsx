import { useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { t } from "@/lib/i18n"
import { Button, FieldLabel, SwitchBox } from "@/components/ui"
import { MultiSelect, type Option } from "@/components/widgets"

type Rule = { include: string[]; exclude: string[] }
type PartKey = "genres" | "countries" | "languages" | "networks"
type Filter = { allow_adult: boolean } & Record<PartKey, Rule>
type View = {
  filter: Filter
  configured: boolean
  default_genres: string[]
  options: { genres: Option[]; genres_source: "tmdb" | "default"; countries: Option[]; languages: Option[]; networks: Option[] }
}

const empty: Rule = { include: [], exclude: [] }

function norm(f: Filter): Filter {
  const r = (x?: Rule): Rule => ({ include: x?.include ?? [], exclude: x?.exclude ?? [] })
  return { allow_adult: !!f.allow_adult, genres: r(f.genres), countries: r(f.countries), languages: r(f.languages), networks: r(f.networks) }
}

function summary(rule: Rule, names: (ids: string[]) => string) {
  const parts: string[] = []
  if (rule.include.length) parts.push(t("Only: {{list}}", { list: names(rule.include) }))
  if (rule.exclude.length) parts.push(rule.include.length ? t("never: {{list}}", { list: names(rule.exclude) }) : t("All except: {{list}}", { list: names(rule.exclude) }))
  return parts.join(", ") || t("No rule")
}

function Part({ title, rule, options, onChange }: { title: string; rule: Rule; options: Option[]; onChange: (r: Rule) => void }) {
  const names = (ids: string[]) => ids.map((id) => options.find((o) => o.id === id)?.name ?? id).join(", ")
  return (
    <div className="space-y-2 rounded-lg border p-3">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="text-sm font-medium">{title}</h3>
        <span className="text-xs text-muted-foreground">{summary(rule, names)}</span>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="min-w-0 space-y-1">
          <FieldLabel>{t("Include")}</FieldLabel>
          <MultiSelect options={options} value={rule.include} omit={rule.exclude} placeholder={t("Any")} search={t("Search")} empty={t("Nothing found")} onChange={(include) => onChange({ ...rule, include })} />
        </div>
        <div className="min-w-0 space-y-1">
          <FieldLabel>{t("Exclude")}</FieldLabel>
          <MultiSelect options={options} value={rule.exclude} omit={rule.include} placeholder={t("None")} search={t("Search")} empty={t("Nothing found")} onChange={(exclude) => onChange({ ...rule, exclude })} />
        </div>
      </div>
    </div>
  )
}

// AutoFilterForm edits the filter for automatically added titles. path is "/panel/auto-filter" or
// "/setup/auto-filter". onSaved runs after a successful save; the buttons stay busy until it settles.
export function AutoFilterForm({ path, saveLabel, onSaved, extra, needsChange }: {
  path: string; saveLabel?: string; onSaved?: () => Promise<unknown> | void; extra?: ReactNode; needsChange?: boolean
}) {
  const view = useQuery({ queryKey: ["auto-filter", path], queryFn: () => api<View>(path), refetchOnMount: "always", staleTime: 0 })
  const [draft, setDraft] = useState<Filter | null>(null)
  const [saved, setSaved] = useState<{ filter: Filter; configured: boolean } | null>(null)
  const [busy, setBusy] = useState(false)
  const v = view.data
  if (view.isError) return <p className="text-sm text-destructive">{(view.error as Error).message}</p>
  if (!v) return <p className="text-sm text-muted-foreground">{t("Loading")}</p>
  const base = norm(saved?.filter ?? v.filter)
  const configured = saved?.configured ?? v.configured
  const f = draft ?? base
  const dirty = !!draft && JSON.stringify(draft) !== JSON.stringify(base)
  const part = (key: PartKey, title: string, options: Option[]) => (
    <Part title={title} rule={f[key] ?? empty} options={options} onChange={(r) => setDraft({ ...f, [key]: r })} />
  )
  async function save() {
    setBusy(true)
    try {
      const res = await api<{ filter: Filter; configured: boolean }>(path, f)
      setSaved({ filter: res.filter, configured: res.configured })
      setDraft(null)
      if (onSaved) await onSaved(); else toast(t("Saved"))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("Something went wrong"))
    } finally { setBusy(false) }
  }
  return (
    <div className="space-y-3">
      <ul className="list-disc space-y-1 ps-5 text-sm text-muted-foreground">
        <li>{t("Each part works alone. Include only: just those. Exclude only: everything except those. Nothing: that part has no say.")}</li>
        <li>{t("A title is added if any part with a rule accepts it. For example, exclude one country but include one network: a series from that country on that network is still added.")}</li>
        <li>{t("An excluded country is left out when a film or series is indexed. The other parts only affect titles added automatically from trending. Seerr requests are still searched.")}</li>
        <li>{t("Adult titles are blocked unless you allow them below, whatever the other rules say.")}</li>
      </ul>
      {!configured && !dirty && <p className="text-sm">{t("No rules are set. Only titles with one of these genres are added: {{list}}.", { list: v.default_genres.join(", ") })}</p>}
      {v.options.genres_source === "default" && <p className="text-sm text-muted-foreground">{t("The genre list from TMDB could not be loaded, so the built-in list is shown.")}</p>}
      {part("genres", t("Genres"), v.options.genres)}
      {part("countries", t("Countries"), v.options.countries)}
      {part("languages", t("Original languages"), v.options.languages)}
      {part("networks", t("Networks and services"), v.options.networks)}
      <label className="flex items-center justify-between text-sm">{t("Allow adult titles")} <SwitchBox label={t("Allow adult titles")} checked={f.allow_adult} onCheckedChange={(allow_adult) => setDraft({ ...f, allow_adult })} /></label>
      <div className="flex flex-wrap justify-end gap-2">
        {extra}
        <Button type="button" size="sm" loading={busy} disabled={busy || (needsChange && !dirty)} onClick={() => void save()}>{saveLabel ?? t("Save")}</Button>
      </div>
    </div>
  )
}
