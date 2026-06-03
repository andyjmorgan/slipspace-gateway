import { Navigate, Route, Routes } from "react-router"
import { ProtectedRoute } from "@/components/protected-route"
import { AppLayout } from "@/components/app-layout"
import { SidebarFooter } from "@/components/sidebar"
import type { TopbarTitles } from "@/components/topbar"
import { CP_NAV } from "./lib/nav-meta"
import { fetchVersion } from "./lib/api"
import { LoginPage } from "./pages/login"
import { FleetPage } from "./pages/fleet"
import { VersionsPage } from "./pages/versions"
import { BindingsPage } from "./pages/bindings"
import { ObservabilityPage } from "./pages/observability"
import { EventDetailPage } from "./pages/event-detail"
import { CpEntityListPage } from "./pages/entity-list"
import { CpBackendListPage } from "./pages/backend-list"
import { CpBackendDetailPage } from "./pages/backend-detail"
import { CpBackendEditorPage } from "./pages/backend-editor"
import { CpRuleListPage } from "./pages/rule-list"
import { CpRuleDetailPage } from "./pages/rule-detail"
import { CpConnectorEditor, CpGroupEditor, CpApiKeyEditor, CpRuleEditor, CpConfigurationEditor } from "./pages/cp-editors"

const CP_TITLES: TopbarTitles = {
  "/fleet": { t: "Fleet", s: "registered gateways · liveness · drift" },
  "/configurations": { t: "Configurations", s: "policy bundles distributed to the fleet" },
  "/rules": { t: "Rules", s: "shared transform library" },
  "/backends": { t: "Backends", s: "upstream connections" },
  "/groups": { t: "Groups", s: "resilience — failover / load-balance" },
  "/connectors": { t: "Connectors", s: "record destinations" },
  "/api-keys": { t: "API keys", s: "issued credentials → configurations" },
  "/bindings": { t: "Bindings", s: "(protocol, model) → backend · read-only" },
  "/observability": { t: "Observability", s: "recent request events across the fleet" },
  "/versions": { t: "Versions", s: "published config versions · rollback" },
}

const cpFooter = <SidebarFooter label="control plane" meta=":8484" />

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <ProtectedRoute>
            <AppLayout nav={CP_NAV} versionFetch={fetchVersion} footer={cpFooter} titles={CP_TITLES} />
          </ProtectedRoute>
        }
      >
        <Route path="/" element={<Navigate to="/fleet" replace />} />
        <Route path="/fleet" element={<FleetPage />} />

        {/* Backends: list → detail → edit, mirroring the gateway console. */}
        <Route path="/backends" element={<CpBackendListPage />} />
        <Route path="/backends/new" element={<CpBackendEditorPage mode="create" />} />
        <Route path="/backends/:name" element={<CpBackendDetailPage />} />
        <Route path="/backends/:name/edit" element={<CpBackendEditorPage mode="edit" />} />

        <Route path="/configurations" element={<CpEntityListPage kind="configuration" />} />
        <Route path="/configurations/new" element={<CpConfigurationEditor mode="create" />} />
        <Route path="/configurations/:name" element={<CpConfigurationEditor mode="edit" />} />

        {/* Rules: list → detail → edit, mirroring the gateway console. */}
        <Route path="/rules" element={<CpRuleListPage />} />
        <Route path="/rules/new" element={<CpRuleEditor mode="create" />} />
        <Route path="/rules/:name" element={<CpRuleDetailPage />} />
        <Route path="/rules/:name/edit" element={<CpRuleEditor mode="edit" />} />

        <Route path="/groups" element={<CpEntityListPage kind="group" />} />
        <Route path="/groups/new" element={<CpGroupEditor mode="create" />} />
        <Route path="/groups/:name" element={<CpGroupEditor mode="edit" />} />

        <Route path="/connectors" element={<CpEntityListPage kind="connector" />} />
        <Route path="/connectors/new" element={<CpConnectorEditor mode="create" />} />
        <Route path="/connectors/:name" element={<CpConnectorEditor mode="edit" />} />

        <Route path="/api-keys" element={<CpEntityListPage kind="api_key" />} />
        <Route path="/api-keys/new" element={<CpApiKeyEditor mode="create" />} />
        <Route path="/api-keys/:name" element={<CpApiKeyEditor mode="edit" />} />

        <Route path="/bindings" element={<BindingsPage />} />
        <Route path="/observability" element={<ObservabilityPage />} />
        <Route path="/observability/:correlation_id" element={<EventDetailPage />} />
        <Route path="/versions" element={<VersionsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/fleet" replace />} />
    </Routes>
  )
}
