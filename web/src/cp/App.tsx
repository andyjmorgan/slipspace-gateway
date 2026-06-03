import { Navigate, Route, Routes } from "react-router"
import { ProtectedRoute } from "@/components/protected-route"
import { LoginPage } from "./pages/login"
import { CpLayout } from "./components/cp-layout"
import { FleetPage } from "./pages/fleet"
import { ConfigPage } from "./pages/config"
import { EntityEditorPage } from "./pages/entity-editor"
import { VersionsPage } from "./pages/versions"

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <ProtectedRoute>
            <CpLayout />
          </ProtectedRoute>
        }
      >
        <Route path="/" element={<Navigate to="/fleet" replace />} />
        <Route path="/fleet" element={<FleetPage />} />
        <Route path="/config" element={<ConfigPage />} />
        <Route path="/config/new" element={<EntityEditorPage mode="create" />} />
        <Route path="/config/:kind/:name" element={<EntityEditorPage mode="edit" />} />
        <Route path="/versions" element={<VersionsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/fleet" replace />} />
    </Routes>
  )
}
