import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"

function files(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) files(p, out)
    else if (/\.(tsx|ts)$/.test(name) && !p.includes("locales")) out.push(p)
  }
  return out
}

const re = /\bt\(\s*(["'`])((?:\\.|(?!\1).)*)\1/g
const keys = new Set()
for (const file of files("web/src")) {
  const s = readFileSync(file, "utf8")
  let m
  while ((m = re.exec(s))) keys.add(m[2])
}
const extra = {
  "Use a channel this account already has, or make a new private one.": {
    fa: "از کانالی که این حساب دارد استفاده کنید، یا یک کانال خصوصی تازه بسازید.",
    de: "Nimm einen Kanal, den dieses Konto schon hat, oder leg einen neuen privaten an.",
    tr: "Bu hesabın zaten sahip olduğu bir kanalı kullanın veya yeni bir özel kanal açın.",
  },
  "Installed": { fa: "نصب شد", de: "Installiert", tr: "Kuruldu" },
  "Setup finished. Open the panel to use this server.": {
    fa: "راه‌اندازی تمام شد. برای استفاده از این سرور پنل را باز کنید.",
    de: "Die Einrichtung ist fertig. Öffne das Panel, um diesen Server zu benutzen.",
    tr: "Kurulum bitti. Bu sunucuyu kullanmak için paneli açın.",
  },
  "Open panel": { fa: "باز کردن پنل", de: "Panel öffnen", tr: "Paneli aç" },
  "API key": { fa: "کلید API", de: "API-Schlüssel", tr: "API anahtarı" },
  "API ID": { fa: "API ID", de: "API-ID", tr: "API ID" },
  "API hash": { fa: "API hash", de: "API-Hash", tr: "API hash" },
  "Channel name": { fa: "نام کانال", de: "Kanalname", tr: "Kanal adı" },
  "Done": { fa: "تمام", de: "Fertig", tr: "Bitti" },
  "Restart": { fa: "راه‌اندازی دوباره", de: "Neu starten", tr: "Yeniden başlat" },
  "Restart failed": { fa: "راه‌اندازی دوباره نشد", de: "Neustart fehlgeschlagen", tr: "Yeniden başlatma olmadı" },
  "Sign in": { fa: "ورود", de: "Anmelden", tr: "Giriş" },
}
for (const k of Object.keys(extra)) keys.add(k)

const missing = []
for (const lang of ["en", "fa", "de", "tr"]) {
  const path = `web/src/locales/${lang}.json`
  const data = JSON.parse(readFileSync(path, "utf8"))
  for (const key of keys) {
    if (key in data && data[key] !== "") continue
    missing.push(lang + " " + key)
    data[key] = lang === "en" ? key : (extra[key]?.[lang] || key)
  }
  const ordered = Object.fromEntries(Object.keys(data).sort((a, b) => a.localeCompare(b)).map((k) => [k, data[k]]))
  writeFileSync(path, JSON.stringify(ordered, null, 2) + "\n")
}
console.log("filled", missing.length)
console.log(missing.slice(0, 40).join("\n"))
