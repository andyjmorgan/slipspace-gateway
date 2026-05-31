// Classification of config-write API failures into the EditorError union the
// write editors render. Kept out of the component module so the components
// file only exports components (react-refresh/only-export-components).

import { APIError } from "@/lib/api"
import type { ConflictError, ValidationFailure } from "@/lib/config-api"

// EditorError is the classified failure shape every write editor renders.
// conflict carries the 409 envelope (name clash or still-referenced
// used_by), validation the 422 detail, generic everything else.
export type EditorError =
  | { kind: "conflict"; message: string; usedBy?: string[]; name?: string }
  | { kind: "validation"; detail: string }
  | { kind: "generic"; message: string }

// classifyWriteError maps an APIError onto the EditorError union by status.
export function classifyWriteError(err: APIError): EditorError {
  if (err.status === 409) {
    const body = err.body as ConflictError | undefined
    return {
      kind: "conflict",
      message: body?.error ?? err.message,
      usedBy: body?.used_by,
      name: body?.name,
    }
  }
  if (err.status === 422) {
    const body = err.body as ValidationFailure | undefined
    return { kind: "validation", detail: body?.detail ?? err.message }
  }
  return { kind: "generic", message: err.message }
}
