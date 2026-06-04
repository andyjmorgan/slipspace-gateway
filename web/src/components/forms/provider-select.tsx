import { SelectField, type SelectOption } from "@/components/forms/field-atoms"
import { useProviders } from "@/lib/config-api"

// ProviderSelectField is the shared constrained dropdown for any field that
// names a configured provider (rule conditions, changeProvider actions, group
// targets, …). Options are sourced live from useProviders so the set always
// reflects the current `providers` block.
//
// A saved value that is not in the live list — a provider renamed or removed
// out of band, or one still loading — is preserved as an explicit
// "(not in provider list)" option rather than silently dropped, so editing an
// unrelated field never clobbers a configured provider name.
export function ProviderSelectField({
  label,
  value,
  onChange,
  hint,
  error,
  allowEmpty = true,
}: {
  label: string
  value: string
  onChange: (next: string) => void
  hint?: string
  error?: string
  allowEmpty?: boolean
}) {
  const { state } = useProviders()
  const loading = state.status === "loading"
  const names = state.status === "ok" ? state.data.map((p) => p.name) : []

  const options: SelectOption[] = []
  if (allowEmpty || value === "") {
    options.push({ value: "", label: loading ? "Loading providers…" : "Select a provider…" })
  }
  for (const name of [...names].sort()) {
    options.push({ value: name })
  }
  // Preserve an unknown/stale saved value so it survives an edit round-trip.
  if (value !== "" && !names.includes(value)) {
    options.push({ value, label: `${value} (not in provider list)` })
  }

  return (
    <SelectField
      label={label}
      value={value}
      onChange={onChange}
      options={options}
      hint={hint}
      error={error}
    />
  )
}
