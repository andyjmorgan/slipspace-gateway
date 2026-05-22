import { Navigate, Route, Routes } from "react-router"
import { LoginPage } from "@/pages/login"
import { DashboardPage } from "@/pages/dashboard"
import { ConfigurationsPage } from "@/pages/configurations"
import { ConfigurationDetailPage } from "@/pages/configuration-detail"
import { RulesPage } from "@/pages/rules"
import { RuleDetailPage } from "@/pages/rule-detail"
import { ProvidersPage } from "@/pages/providers"
import { ProviderDetailPage } from "@/pages/provider-detail"
import { RoutesPage } from "@/pages/routes"
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
        <Route path="/rules/:name" element={<RuleDetailPage />} />
        <Route path="/providers" element={<ProvidersPage />} />
        <Route path="/providers/:name" element={<ProviderDetailPage />} />
        <Route path="/routes" element={<RoutesPage />} />
        <Route path="/policies" element={<PoliciesPage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  )
}
