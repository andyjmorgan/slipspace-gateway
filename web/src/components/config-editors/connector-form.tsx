import { PanelCard, PanelHead } from "@/components/atoms/card"
import { TextField, NumberField, SelectField, CheckboxField } from "@/components/forms/field-atoms"
import {
  AZURE_AUTH_MODES,
  CONNECTOR_TYPE_OPTIONS,
  S3_AUTH_MODES,
  type ConnectorAuth,
  type ConnectorFormState,
} from "./connector-form-model"

// ConnectorFormFields is the shared connector form body — identity + type, then
// the type-specific destination + auth panels. Used by both consoles.
export function ConnectorFormFields({
  value: form,
  onChange,
  nameEditable,
}: {
  value: ConnectorFormState
  onChange: (next: ConnectorFormState) => void
  nameEditable: boolean
}) {
  const setAuth = (next: Partial<ConnectorAuth>) => onChange({ ...form, auth: { ...form.auth, ...next } })
  return (
    <>
      <PanelCard>
        <PanelHead title="Connector" sub="identity and type" />
        <div className="px-4 py-4 flex flex-col gap-3">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => onChange({ ...form, name: v })}
            placeholder="audit-s3"
            mono
            hint={nameEditable ? "Unique across connectors. Referenced by connector bindings." : "Names are immutable post-create."}
          />
          <SelectField label="Type" value={form.type} options={CONNECTOR_TYPE_OPTIONS} onChange={(t) => onChange({ ...form, type: t })} />
        </div>
      </PanelCard>

      {form.type === "s3" && (
        <PanelCard>
          <PanelHead title="S3 destination" sub="bucket + region; endpoint_url for S3-compatible backends" />
          <div className="px-4 py-4 flex flex-col gap-3">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <TextField label="Bucket" value={form.bucket ?? ""} onChange={(v) => onChange({ ...form, bucket: v })} placeholder="sluice-artifacts" mono />
              <TextField label="Region" value={form.region ?? ""} onChange={(v) => onChange({ ...form, region: v })} placeholder="us-east-1" mono />
              <TextField label="Prefix" value={form.prefix ?? ""} onChange={(v) => onChange({ ...form, prefix: v })} placeholder="records/" mono />
              <TextField label="Endpoint URL" value={form.endpoint_url ?? ""} onChange={(v) => onChange({ ...form, endpoint_url: v })} placeholder="https://s3.donkeywork.dev" mono hint="Blank = real AWS S3." />
            </div>
            <CheckboxField
              label="Use path-style addressing"
              checked={form.use_path_style ?? false}
              onChange={(c) => onChange({ ...form, use_path_style: c })}
              hint="MinIO / SeaweedFS default to path-style; AWS prefers virtual-hosted."
            />
            <SelectField label="Auth mode" value={form.auth.mode} options={S3_AUTH_MODES} onChange={(m) => setAuth({ mode: m })} />
            {form.auth.mode === "static" && (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <TextField label="Access key ID ref" value={form.auth.access_key_id_ref ?? ""} onChange={(v) => setAuth({ access_key_id_ref: v })} placeholder="env:AWS_ACCESS_KEY_ID" mono />
                <TextField label="Secret access key ref" value={form.auth.secret_access_key_ref ?? ""} onChange={(v) => setAuth({ secret_access_key_ref: v })} placeholder="env:AWS_SECRET_ACCESS_KEY" mono />
              </div>
            )}
            {form.auth.mode === "assume_role" && (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <TextField label="Role ARN" value={form.auth.role_arn ?? ""} onChange={(v) => setAuth({ role_arn: v })} placeholder="arn:aws:iam::…:role/…" mono />
                <TextField label="External ID ref" value={form.auth.external_id_ref ?? ""} onChange={(v) => setAuth({ external_id_ref: v })} placeholder="env:EXTERNAL_ID" mono hint="Optional but recommended for cross-account." />
              </div>
            )}
          </div>
        </PanelCard>
      )}

      {form.type === "azure_blob" && (
        <PanelCard>
          <PanelHead title="Azure Blob destination" sub="storage account + container" />
          <div className="px-4 py-4 flex flex-col gap-3">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <TextField label="Account" value={form.account ?? ""} onChange={(v) => onChange({ ...form, account: v })} placeholder="sluicestore" mono />
              <TextField label="Container" value={form.container ?? ""} onChange={(v) => onChange({ ...form, container: v })} placeholder="records" mono />
            </div>
            <SelectField label="Auth mode" value={form.auth.mode} options={AZURE_AUTH_MODES} onChange={(m) => setAuth({ mode: m })} />
            {form.auth.mode === "sas_token" && (
              <TextField label="SAS token ref" value={form.auth.sas_token_ref ?? ""} onChange={(v) => setAuth({ sas_token_ref: v })} placeholder="env:AZURE_SAS_TOKEN" mono />
            )}
            {form.auth.mode === "account_key" && (
              <TextField label="Account key ref" value={form.auth.account_key_ref ?? ""} onChange={(v) => setAuth({ account_key_ref: v })} placeholder="env:AZURE_ACCOUNT_KEY" mono />
            )}
          </div>
        </PanelCard>
      )}

      {(form.type === "webhook" || form.type === "controlplane") && (
        <PanelCard>
          <PanelHead
            title={form.type === "controlplane" ? "Control-plane destination" : "Webhook destination"}
            sub={form.type === "controlplane" ? "the CP's /api/v1/ingest/segment endpoint + bootstrap token ref" : "HTTPS endpoint + HMAC signing key ref"}
          />
          <div className="px-4 py-4 flex flex-col gap-3">
            <TextField label="URL" value={form.url ?? ""} onChange={(v) => onChange({ ...form, url: v })} placeholder={form.type === "controlplane" ? "http://sluice-controlplane:8484/api/v1/ingest/segment" : "https://receiver.example/sluice"} mono />
            <TextField label={form.type === "controlplane" ? "Secret ref (bootstrap token)" : "Secret ref (HMAC key)"} value={form.secret_ref ?? ""} onChange={(v) => onChange({ ...form, secret_ref: v })} placeholder={form.type === "controlplane" ? "env:SLUICE_CP_TOKEN" : "env:WEBHOOK_HMAC_KEY"} mono />
            <NumberField label="Timeout (ms)" value={form.timeout_ms ?? null} onChange={(n) => onChange({ ...form, timeout_ms: n ?? undefined })} placeholder="5000" hint="Per-call HTTP timeout. 0 < timeout <= 60000." />
          </div>
        </PanelCard>
      )}
    </>
  )
}
