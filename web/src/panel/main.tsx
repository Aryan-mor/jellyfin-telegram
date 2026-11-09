import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { HashRouter, Route, Routes } from "react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "sonner"
import "@/index.css"
import { ConfirmHost } from "@/components/widgets"
import { I18nProvider, useUiLang } from "@/lib/i18n"
import { Shell } from "@/panel/shell"
import { Accounts, ApiPage, Away, Bots, Channel, Downloads, HomeVideos, Local, Mirrors, News, Overview, Shared, Sources, Subtitles, Telegram } from "@/panel/pages"

const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })

function ToastHost() {
  useUiLang()
  return <Toaster position={document.documentElement.dir === "rtl" ? "bottom-left" : "bottom-right"} />
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <I18nProvider>
      <HashRouter>
        <ToastHost />
        <ConfirmHost />
        <Routes>
          <Route element={<Shell />}>
            <Route index element={<Overview />} />
            <Route path="accounts" element={<Accounts />} />
            <Route path="channel" element={<Channel />} />
            <Route path="channel/mirrors" element={<Mirrors />} />
            <Route path="channel/shared" element={<Shared />} />
            <Route path="channel/home" element={<HomeVideos />} />
            <Route path="channel/subtitles" element={<Subtitles />} />
            <Route path="telegram" element={<Telegram />} />
            <Route path="bots" element={<Bots />} />
            <Route path="api" element={<ApiPage />} />
            <Route path="sources" element={<Sources />} />
            <Route path="news" element={<News />} />
            <Route path="local" element={<Local />} />
            <Route path="downloads" element={<Downloads />} />
            <Route path="away" element={<Away />} />
          </Route>
        </Routes>
      </HashRouter>
      </I18nProvider>
    </QueryClientProvider>
  </StrictMode>,
)
