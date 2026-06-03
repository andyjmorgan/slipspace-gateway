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
import { CpEntityListPage } from "./pages/entity-list"
import { EntityEditorPage } from "./pages/entity-editor"
import { CpBackendEditorPage } from "./pages/backend-editor"

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

        {/* Backends have a structured editor; the rest use the generic JSON
            editor until their parity slices land. */}
        <Route path="/backends" element={<CpEntityListPage kind="backend" />} />
        <Route path="/backends/new" element={<CpBackendEditorPage mode="create" />} />
        <Route path="/backends/:name" element={<CpBackendEditorPage mode="edit" />} />

        <Route path="/configurations" element={<CpEntityListPage kind="configuration" />} />
        <Route path="/configurations/new" element={<EntityEditorPage mode="create" kind="configuration" />} />
        <Route path="/configurations/:name" element={<EntityEditorPage mode="edit" kind="configuration" />} />

        <Route path="/rules" element={<CpEntityListPage kind="rule" />} />
        <Route path="/rules/new" element={<EntityEditorPage mode="create" kind="rule" />} />
        <Route path="/rules/:name" element={<EntityEditorPage mode="edit" kind="rule" />} />

        <Route path="/groups" element={<CpEntityListPage kind="group" />} />
        <Route path="/groups/new" element={<EntityEditorPage mode="create" kind="group" />} />
        <Route path="/groups/:name" element={<EntityEditorPage mode="edit" kind="group" />} />

        <Route path="/connectors" element={<CpEntityListPage kind="connector" />} />
        <Route path="/connectors/new" element={<EntityEditorPage mode="create" kind="connector" />} />
        <Route path="/connectors/:name" element={<EntityEditorPage mode="edit" kind="connector" />} />

        <Route path="/api-keys" element={<CpEntityListPage kind="api_key" />} />
        <Route path="/api-keys/new" element={<EntityEditorPage mode="create" kind="api_key" />} />
        <Route path="/api-keys/:name" element={<EntityEditorPage mode="edit" kind="api_key" />} />

        <Route path="/bindings" element={<BindingsPage />} />
        <Route path="/observability" element={<ObservabilityPage />} />
        <Route path="/versions" element={<VersionsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/fleet" replace />} />
    </Routes>
  )
}
