import { Navigate, Route, Routes } from "react-router"
import { LoginPage } from "@/pages/login"
import { DashboardPage } from "@/pages/dashboard"
import { ConfigurationsPage } from "@/pages/configurations"
import { ConfigurationDetailPage } from "@/pages/configuration-detail"
import { RulesPage } from "@/pages/rules"
import { RuleDetailPage } from "@/pages/rule-detail"
import { RuleEditorPage } from "@/pages/rule-editor"
import { BackendsPage } from "@/pages/backends"
import { BackendDetailPage } from "@/pages/backend-detail"
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
        <Route path="/configurations/:name" element={<ConfigurationDetailPage />} />
        <Route path="/rules" element={<RulesPage />} />
        <Route path="/rules/new" element={<RuleEditorPage mode="create" />} />
        <Route path="/rules/:name/edit" element={<RuleEditorPage mode="edit" />} />
        <Route path="/rules/:name" element={<RuleDetailPage />} />
        <Route path="/backends" element={<BackendsPage />} />
        <Route path="/backends/:name" element={<BackendDetailPage />} />
        <Route path="/bindings" element={<BindingsPage />} />
        <Route path="/policies" element={<PoliciesPage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  )
}
