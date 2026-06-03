import { useState } from "react"
import { UploadCloud } from "lucide-react"
import { Button } from "@/components/ui/button"
import { apiErrorText } from "../lib/api"
import { publish } from "../lib/config-api"

// PublishBar is the control-plane's global "compose + validate + activate"
// action, surfaced in every config list-page header (staged edits apply to the
// fleet only on publish). Owns its own result banner.
export function PublishBar() {
  const [busy, setBusy] = useState(false)
  const [banner, setBanner] = useState<{ ok: boolean; text: string } | null>(null)

  const onPublish = async () => {
    setBusy(true)
    setBanner(null)
    try {
      const res = await publish()
      setBanner({ ok: true, text: `Published version ${res.version.slice(0, 8)} (${res.hash.slice(0, 12)}…)` })
    } catch (e) {
      setBanner({ ok: false, text: apiErrorText(e) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col items-end gap-2">
      <Button size="sm" onClick={onPublish} disabled={busy}>
        <UploadCloud />
        {busy ? "Publishing…" : "Publish"}
      </Button>
      {banner && (
        <div
          className="rounded-[var(--radius)] px-2.5 py-1.5 text-[12px] border max-w-[420px]"
          style={
            banner.ok
              ? { color: "var(--ok)", background: "var(--ok-bg)", borderColor: "color-mix(in oklab, var(--ok) 30%, var(--border))" }
              : { color: "var(--err)", background: "var(--err-bg)", borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))" }
          }
        >
          {banner.text}
        </div>
      )}
    </div>
  )
}
