// Editable mirror of components/atoms/action-view.tsx. One sub-form
// per action type, dispatched on the JSON `type` discriminator from
// the top-level ActionForm. The parent RuleEditorPage owns the
// actions array (Add / Remove / Reorder controls) — ActionForm only
// edits a single action.
//
// As with ConditionForm, values pass as Record<string, unknown> so
// the form's output is the wire shape directly.

import {
  CheckboxField,
  FieldGrid,
  FieldRow,
  NumberField,
  SelectField,
  TextField,
  TextareaField,
  type SelectOption,
} from "@/components/forms/field-atoms"
import { ProviderSelectField } from "@/components/forms/provider-select"

export type ActionValue = Record<string, unknown>

const HEADER_OPS: SelectOption[] = [
  { value: "Set" },
  { value: "Append" },
  { value: "Prepend" },
  { value: "Remove" },
]

const STATUS_BODY_TYPES: SelectOption[] = [
  { value: "text" },
  { value: "json" },
  { value: "html" },
]

// ACTION_TYPES is the canonical list surfaced in the type-selector
// dropdown. Ordering follows the read-side ActionKindBadge buckets:
// rewrite first, then augment, then terminating, then body-rewrite.
const ACTION_TYPES: SelectOption[] = [
  { value: "changeProvider", label: "change provider" },
  { value: "changeModelName", label: "change model name" },
  { value: "changeUrl", label: "change URL" },
  { value: "changeApiKey", label: "change API key" },
  { value: "setHeader", label: "set / append / remove header" },
  { value: "appendQueryString", label: "append query string" },
  { value: "addTag", label: "add tag" },
  { value: "useResiliencePolicy", label: "use resilience policy" },
  { value: "returnStatusCode", label: "return status code (terminating)" },
  { value: "llmImpersonation", label: "LLM impersonation (terminating)" },
  { value: "rewriteField", label: "rewrite body field" },
  { value: "appendField", label: "append to body field (array)" },
  { value: "removeField", label: "remove body field" },
]

function defaultsFor(type: string): ActionValue {
  switch (type) {
    case "changeProvider":
      return { type, new_provider: "" }
    case "changeModelName":
      return { type, new_model_name: "" }
    case "changeUrl":
      return { type, new_url: "" }
    case "changeApiKey":
      return { type, api_key: "", use_sluice_key: false }
    case "setHeader":
      return { type, header_action: "Set", header_name: "", header_value: "" }
    case "appendQueryString":
      return { type, parameter_name: "", parameter_value: "" }
    case "addTag":
      return { type, tag: "" }
    case "useResiliencePolicy":
      return { type, policy_name: "" }
    case "returnStatusCode":
      return { type, status_code: 200, body_type: "text", body: "" }
    case "llmImpersonation":
      return { type, model: "", message: "" }
    case "rewriteField":
      return { type, target: "", value: "" }
    case "appendField":
      return { type, target: "", value: "" }
    case "removeField":
      return { type, target: "" }
    default:
      return { type }
  }
}

export function emptyAction(): ActionValue {
  return defaultsFor("setHeader")
}

export function ActionForm({
  value,
  onChange,
}: {
  value: ActionValue
  onChange: (next: ActionValue) => void
}) {
  const type = String(value?.type ?? "setHeader")
  return (
    <div className="flex flex-col gap-3">
      <SelectField
        label="Action type"
        value={type}
        options={ACTION_TYPES}
        onChange={(t) => onChange(defaultsFor(t))}
      />
      <Subform value={value} onChange={onChange} />
    </div>
  )
}

function Subform({ value, onChange }: { value: ActionValue; onChange: (v: ActionValue) => void }) {
  const type = String(value?.type ?? "")
  switch (type) {
    case "changeProvider":
      return (
        <ProviderSelectField
          label="New provider"
          value={String(value.new_provider ?? "")}
          onChange={(v) => onChange({ ...value, new_provider: v })}
        />
      )
    case "changeModelName":
      return (
        <TextField
          label="New model name"
          value={String(value.new_model_name ?? "")}
          onChange={(v) => onChange({ ...value, new_model_name: v })}
          placeholder="claude-3-5-sonnet-latest"
          mono
        />
      )
    case "changeUrl":
      return (
        <TextField
          label="New URL"
          value={String(value.new_url ?? "")}
          onChange={(v) => onChange({ ...value, new_url: v })}
          placeholder="https://example.com/v1/chat/completions"
          mono
        />
      )
    case "changeApiKey":
      return (
        <>
          <TextField
            label="API key (plaintext)"
            value={String(value.api_key ?? "")}
            onChange={(v) => onChange({ ...value, api_key: v })}
            hint="Written verbatim to policy.yaml. Stored on disk."
            mono
          />
          <CheckboxField
            label="Use the inbound Sluice key as the upstream credential"
            checked={Boolean(value.use_sluice_key)}
            onChange={(b) => onChange({ ...value, use_sluice_key: b })}
            hint="If checked, the api_key field is ignored and the inbound credential is forwarded verbatim."
          />
        </>
      )
    case "setHeader": {
      const op = String(value.header_action ?? "Set")
      const showValue = op !== "Remove"
      return (
        <>
          <FieldGrid>
            <SelectField
              label="Operation"
              value={op}
              options={HEADER_OPS}
              onChange={(o) => onChange({ ...value, header_action: o })}
            />
            <TextField
              label="Header name"
              value={String(value.header_name ?? "")}
              onChange={(v) => onChange({ ...value, header_name: v })}
              placeholder="X-Sluice-Configuration"
              mono
            />
          </FieldGrid>
          {showValue && (
            <TextField
              label="Header value"
              value={String(value.header_value ?? "")}
              onChange={(v) => onChange({ ...value, header_value: v })}
              mono
            />
          )}
        </>
      )
    }
    case "appendQueryString":
      return (
        <FieldGrid>
          <TextField
            label="Parameter name"
            value={String(value.parameter_name ?? "")}
            onChange={(v) => onChange({ ...value, parameter_name: v })}
            placeholder="api-version"
            mono
          />
          <TextField
            label="Parameter value"
            value={String(value.parameter_value ?? "")}
            onChange={(v) => onChange({ ...value, parameter_value: v })}
            mono
          />
        </FieldGrid>
      )
    case "addTag":
      return (
        <TextField
          label="Tag"
          value={String(value.tag ?? "")}
          onChange={(v) => onChange({ ...value, tag: v })}
          placeholder="claude-code"
          mono
        />
      )
    case "useResiliencePolicy":
      return (
        <TextField
          label="Policy name"
          value={String(value.policy_name ?? "")}
          onChange={(v) => onChange({ ...value, policy_name: v })}
          placeholder="cross-provider-failover"
          mono
          hint="Must match an entry in the resilience_policies library, or be empty to clear an earlier-set policy."
        />
      )
    case "returnStatusCode":
      return (
        <>
          <FieldGrid>
            <NumberField
              label="Status code"
              value={(value.status_code as number | null) ?? null}
              onChange={(n) => onChange({ ...value, status_code: n })}
              placeholder="429"
            />
            <SelectField
              label="Body type"
              value={String(value.body_type ?? "text")}
              options={STATUS_BODY_TYPES}
              onChange={(b) => onChange({ ...value, body_type: b })}
            />
          </FieldGrid>
          <TextareaField
            label="Body"
            value={String(value.body ?? "")}
            onChange={(v) => onChange({ ...value, body: v })}
            rows={3}
            placeholder='{"error": "rate limit"}'
          />
        </>
      )
    case "llmImpersonation":
      return (
        <>
          <TextField
            label="Model"
            value={String(value.model ?? "")}
            onChange={(v) => onChange({ ...value, model: v })}
            placeholder="gpt-4o-mini"
            mono
          />
          <TextareaField
            label="Message"
            value={String(value.message ?? "")}
            onChange={(v) => onChange({ ...value, message: v })}
            rows={3}
          />
        </>
      )
    case "rewriteField":
    case "appendField":
      return <BodyFieldRewriteForm value={value} onChange={onChange} />
    case "removeField":
      return (
        <TextField
          label="Target (sjson path)"
          value={String(value.target ?? "")}
          onChange={(v) => onChange({ ...value, target: v })}
          placeholder="request.body.user"
          mono
        />
      )
    default:
      return (
        <FieldRow label="Unknown action type">
          <span className="text-[12px] text-[color:var(--text-3)]">
            No form available for this action type. Switch to JSON to edit.
          </span>
        </FieldRow>
      )
  }
}

// BodyFieldRewriteForm edits a (target, value) pair where value can be
// any JSON type. The textarea stores the value as a JSON literal —
// user types `true`, `"foo"`, `42`, `null`, or `{"k":"v"}`. We parse
// on every keystroke and surface a syntax error inline; the parsed
// value is what flows into onChange.
function BodyFieldRewriteForm({ value, onChange }: { value: ActionValue; onChange: (v: ActionValue) => void }) {
  const target = String(value.target ?? "")
  const literal = formatLiteral(value.value)
  const parsed = parseLiteral(literal)
  return (
    <>
      <TextField
        label="Target (sjson path)"
        value={target}
        onChange={(v) => onChange({ ...value, target: v })}
        placeholder="request.body.stream_options.include_usage"
        mono
      />
      <TextareaField
        label="Value (JSON literal)"
        value={literal}
        onChange={(v) => {
          const next = parseLiteral(v)
          // Store the raw literal as well so the textarea round-trips
          // before parsing — we keep the parsed form in `value.value`,
          // and on parse failure leave the previously parsed value
          // alone so a half-typed token doesn't blank the field.
          if (next.ok) {
            onChange({ ...value, value: next.parsed })
          }
        }}
        rows={3}
        error={parsed.ok ? undefined : parsed.error}
        hint='Write JSON literally: `true`, `"foo"`, `42`, `null`, `{"role":"system"}`.'
      />
    </>
  )
}

// formatLiteral renders the stored value as a JSON literal so the
// textarea always has a parseable starting point.
function formatLiteral(v: unknown): string {
  if (v === undefined) return ""
  try {
    return JSON.stringify(v)
  } catch {
    return ""
  }
}

type ParseResult = { ok: true; parsed: unknown } | { ok: false; error: string }

function parseLiteral(s: string): ParseResult {
  const trimmed = s.trim()
  if (trimmed === "") return { ok: true, parsed: "" }
  try {
    return { ok: true, parsed: JSON.parse(trimmed) }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : String(e) }
  }
}
