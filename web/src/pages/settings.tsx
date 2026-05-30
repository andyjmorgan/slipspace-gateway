import { useCallback, useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { Download } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
} from "@/components/atoms/page-states"
import { apiFetch, APIError, UnauthorizedError } from "@/lib/api"
import { auth } from "@/lib/auth"

type ConfigExportFile = {
  name: string
  content: string
  size_bytes: number
}

type ConfigExportFilesResponse = {
  files: ConfigExportFile[]
}

type FetchState =
  | { status: "loading" }
  | { status: "ok"; files: ConfigExportFile[] }
  | { status: "error"; message: string }
  | { status: "unauthorized" }

export function SettingsPage() {
  const [state, setState] = useState<FetchState>({ status: "loading" })
  const [downloading, setDownloading] = useState(false)
  const [downloadError, setDownloadError] = useState<string | null>(null)
  const nav = useNavigate()

  useEffect(() => {
    let cancelled = false
    apiFetch<ConfigExportFilesResponse>("/api/v1/config/export/files")
      .then((res) => {
        if (cancelled) return
        setState({ status: "ok", files: res.files })
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof UnauthorizedError) {
          setState({ status: "unauthorized" })
          return
        }
        if (err instanceof APIError) {
          setState({ status: "error", message: `${err.status}: ${err.message}` })
          return
        }
        setState({ status: "error", message: String(err) })
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (state.status === "unauthorized") {
      nav("/login", { replace: true })
    }
  }, [state, nav])

  const handleDownload = useCallback(async () => {
    setDownloading(true)
    setDownloadError(null)
    try {
      const header = auth.header()
      const headers: Record<string, string> = {}
      if (header) headers.Authorization = header
      const res = await fetch("/admin/api/v1/config/export/download", { headers })
      if (res.status === 401) {
        auth.clear()
        nav("/login", { replace: true })
        return
      }
      if (!res.ok) {
        const text = await res.text().catch(() => "")
        throw new Error(`${res.status}: ${text || res.statusText}`)
      }
      const blob = await res.blob()
      const filename = parseFilename(res.headers.get("Content-Disposition"))
      triggerDownload(blob, filename)
    } catch (e) {
      setDownloadError(e instanceof Error ? e.message : String(e))
    } finally {
      setDownloading(false)
    }
  }, [nav])

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Settings"
        sub="Inspect and export the gateway's merged on-disk configuration. Secrets are redacted to ***."
      />

      <PanelCard>
        <PanelHead
          title="Export configuration"
          action={
            <Button onClick={handleDownload} disabled={downloading} size="sm">
              <Download className={downloading ? "animate-pulse" : undefined} />
              {downloading ? "Preparing…" : "Download .zip"}
            </Button>
          }
        />
        <div className="px-4 pb-3 text-[12.5px] text-[color:var(--text-3)]">
          Bundles every YAML file the loader sees into a single ZIP with a{" "}
          <span className="mono">MANIFEST.txt</span> header. Every{" "}
          <span className="mono">api_keys[].secret</span>,{" "}
          <span className="mono">credentials</span> value, and{" "}
          <span className="mono">admin.password</span> is replaced by{" "}
          <span className="mono">***</span> before the file leaves the gateway.
        </div>
        {downloadError && (
          <div className="px-4 pb-3 text-[12.5px]" style={{ color: "var(--err)" }}>
            Download failed: <span className="mono">{downloadError}</span>
          </div>
        )}
      </PanelCard>

      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.files.length === 0 && (
        <EmptyPanel message="No configuration files loaded." />
      )}
      {state.status === "ok" && state.files.length > 0 && (
        <FilesTabs files={state.files} />
      )}
    </div>
  )
}

function FilesTabs({ files }: { files: ConfigExportFile[] }) {
  return (
    <PanelCard>
      <Tabs defaultValue={files[0].name} className="gap-3 p-4">
        <TabsList variant="line">
          {files.map((f) => (
            <TabsTrigger key={f.name} value={f.name}>
              <span className="mono text-[12.5px]">{f.name}</span>
            </TabsTrigger>
          ))}
        </TabsList>
        {files.map((f) => (
          <TabsContent key={f.name} value={f.name}>
            <div className="mb-2 text-[11.5px] text-[color:var(--text-3)]">
              <span className="mono">{f.name}</span> · {f.size_bytes} bytes ·
              redacted
            </div>
            <pre className="mono text-[12.5px] leading-[1.55] rounded-[var(--radius-md)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-3 overflow-auto max-h-[60vh] whitespace-pre">
              {f.content}
            </pre>
          </TabsContent>
        ))}
      </Tabs>
    </PanelCard>
  )
}

// parseFilename extracts the filename from a Content-Disposition header.
// Falls back to "sluice-config.zip" when the header is absent or malformed —
// the server always sets it on the happy path, but defending against a
// reverse-proxy stripping headers costs little.
function parseFilename(disposition: string | null): string {
  if (!disposition) return "sluice-config.zip"
  const match = /filename="?([^";]+)"?/.exec(disposition)
  return match ? match[1] : "sluice-config.zip"
}

function triggerDownload(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  // Browsers may keep the blob alive until the anchor is GC'd; revoking on
  // the next tick is the documented pattern and avoids a leak in long-lived
  // sessions where operators export repeatedly.
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
