// Per-type control-plane structured editors. Each is a thin binding of a shared
// *FormFields component (also used by the gateway console) to the CP's
// stage-then-publish entity API via CpStructuredEditor.

import { ConnectorFormFields } from "@/components/config-editors/connector-form"
import {
  connectorFormFromContract,
  connectorFormToContract,
  emptyConnectorForm,
} from "@/components/config-editors/connector-form-model"
import { GroupFormFields } from "@/components/config-editors/group-form"
import {
  groupFormFromContract,
  groupFormToContract,
  emptyGroupForm,
} from "@/components/config-editors/group-form-model"
import { ApiKeyFormFields } from "@/components/config-editors/api-key-form"
import {
  apiKeyFormFromContract,
  apiKeyFormToContract,
  emptyApiKeyForm,
} from "@/components/config-editors/api-key-form-model"
import { RuleFormFields } from "@/components/config-editors/rule-form"
import {
  ruleFormFromContract,
  ruleFormToContract,
  emptyRuleForm,
} from "@/components/config-editors/rule-form-model"
import { ConfigurationFormFields } from "@/components/config-editors/configuration-form"
import {
  configFormFromContract,
  configFormToContract,
  emptyConfigForm,
} from "@/components/config-editors/configuration-form-model"
import { CpStructuredEditor } from "./structured-editor"

export function CpConnectorEditor({ mode }: { mode: "create" | "edit" }) {
  return (
    <CpStructuredEditor
      mode={mode}
      kind="connector"
      FormFields={ConnectorFormFields}
      emptyForm={emptyConnectorForm}
      fromContract={connectorFormFromContract}
      toContract={connectorFormToContract}
      nameOf={(f) => f.name}
      sub="A reusable spool destination — s3 / azure_blob / webhook / controlplane."
    />
  )
}

export function CpGroupEditor({ mode }: { mode: "create" | "edit" }) {
  return (
    <CpStructuredEditor
      mode={mode}
      kind="group"
      FormFields={GroupFormFields}
      emptyForm={emptyGroupForm}
      fromContract={groupFormFromContract}
      toContract={groupFormToContract}
      nameOf={(f) => f.name}
      sub="A resilience group — failover / load-balance across backend targets with a circuit breaker."
    />
  )
}

export function CpApiKeyEditor({ mode }: { mode: "create" | "edit" }) {
  return (
    <CpStructuredEditor
      mode={mode}
      kind="api_key"
      FormFields={ApiKeyFormFields}
      emptyForm={emptyApiKeyForm}
      fromContract={apiKeyFormFromContract}
      toContract={apiKeyFormToContract}
      nameOf={(f) => f.name}
      sub="An issued credential resolving to a configuration."
    />
  )
}

export function CpRuleEditor({ mode }: { mode: "create" | "edit" }) {
  return (
    <CpStructuredEditor
      mode={mode}
      kind="rule"
      FormFields={RuleFormFields}
      emptyForm={emptyRuleForm}
      fromContract={ruleFormFromContract}
      toContract={ruleFormToContract}
      nameOf={(f) => f.name}
      afterSaveTo={(name) => `/rules/${encodeURIComponent(name)}`}
      sub="A transform rule — a condition and ordered actions, attached to configurations."
    />
  )
}

export function CpConfigurationEditor({ mode }: { mode: "create" | "edit" }) {
  return (
    <CpStructuredEditor
      mode={mode}
      kind="configuration"
      FormFields={ConfigurationFormFields}
      emptyForm={emptyConfigForm}
      fromContract={configFormFromContract}
      toContract={configFormToContract}
      nameOf={(f) => f.name}
      sub="A policy bundle — credentials, bindings, attached rules, and tags distributed to the fleet."
    />
  )
}
