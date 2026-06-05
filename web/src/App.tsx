import { Navigate, Route, Routes } from "react-router"
import { LoginPage } from "@/pages/login"
import { DashboardPage } from "@/pages/dashboard"
import { ConfigurationsPage } from "@/pages/configurations"
import { ConfigurationDetailPage } from "@/pages/configuration-detail"
import { RulesPage } from "@/pages/rules"
import { RuleDetailPage } from "@/pages/rule-detail"
import { RuleEditorPage } from "@/pages/rule-editor"
import { ProvidersPage } from "@/pages/providers"
import { ProviderDetailPage } from "@/pages/provider-detail"
import { ProviderEditorPage } from "@/pages/provider-editor"
import { ConfigurationEditorPage } from "@/pages/configuration-editor"
import { GroupEditorPage } from "@/pages/group-editor"
import { ConnectorsPage } from "@/pages/connectors"
import { ConnectorEditorPage } from "@/pages/connector-editor"
import { APIKeysPage } from "@/pages/api-keys"
import { BindingsPage } from "@/pages/bindings"
import { MessagesPage } from "@/pages/messages"
import { PoliciesPage } from "@/pages/policies"
import { SettingsPage } from "@/pages/settings"
import { AppLayout } from "@/components/app-layout"
import { ProtectedRoute } from "@/components/protected-route"

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <ProtectedRoute>
            <AppLayout />
          </ProtectedRoute>
        }
      >
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="/dashboard" element={<DashboardPage />} />
        <Route path="/messages" element={<MessagesPage />} />
        <Route path="/configurations" element={<ConfigurationsPage />} />
        <Route path="/configurations/new" element={<ConfigurationEditorPage mode="create" />} />
        <Route path="/configurations/:name/edit" element={<ConfigurationEditorPage mode="edit" />} />
        <Route path="/configurations/:name" element={<ConfigurationDetailPage />} />
        <Route path="/rules" element={<RulesPage />} />
        <Route path="/rules/new" element={<RuleEditorPage mode="create" />} />
        <Route path="/rules/:name/edit" element={<RuleEditorPage mode="edit" />} />
        <Route path="/rules/:name" element={<RuleDetailPage />} />
        <Route path="/providers" element={<ProvidersPage />} />
        <Route path="/providers/new" element={<ProviderEditorPage mode="create" />} />
        <Route path="/providers/:name/edit" element={<ProviderEditorPage mode="edit" />} />
        <Route path="/providers/:name" element={<ProviderDetailPage />} />
        <Route path="/groups/new" element={<GroupEditorPage mode="create" />} />
        <Route path="/groups/:name/edit" element={<GroupEditorPage mode="edit" />} />
        <Route path="/connectors" element={<ConnectorsPage />} />
        <Route path="/connectors/new" element={<ConnectorEditorPage mode="create" />} />
        <Route path="/connectors/:name/edit" element={<ConnectorEditorPage mode="edit" />} />
        <Route path="/api-keys" element={<APIKeysPage />} />
        <Route path="/bindings" element={<BindingsPage />} />
        <Route path="/policies" element={<PoliciesPage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  )
}
