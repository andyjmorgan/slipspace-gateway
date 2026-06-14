import { ArrowUpRight } from "lucide-react"
import { TableScroll } from "@/components/atoms/card"
import { SkeletonRows } from "@/components/atoms/skeleton"
import { fmt } from "@/lib/fmt"
import type { FindingRow } from "@/lib/findings"
import { Dash } from "./messages-table"

// checkTypeColor maps a detector check type to its semantic token, so injection
// reads as the highest-severity class. Unknown check types fall back to neutral.
const checkTypeColor: Record<string, string> = {
  injection: "var(--err)",
  toxicity: "var(--warn)",
  pii: "var(--violet)",
}

// CheckTypeBadge renders a finding's check type as a bordered chip in its
// severity color — the same chip idiom the inspector's Security pane uses for a
// verdict state, applied here per finding.
function CheckTypeBadge({ checkType }: { checkType: string }) {
  const color = checkTypeColor[checkType] ?? "var(--text-3)"
  return (
    <span
      className="inline-flex items-center px-1.5 py-0.5 rounded-[4px] text-[10.5px] mono uppercase tracking-[0.04em] border"
      style={{ color, borderColor: color }}
    >
      {checkType}
    </span>
  )
}

// ScoreCell renders a detector score [0,1] tinted by severity: a high score
// reads as error, a moderate one as warn, low as muted — so the eye lands on the
// worst hits without reading every number.
function ScoreCell({ score }: { score: number }) {
  const color = score >= 0.8 ? "var(--err)" : score >= 0.5 ? "var(--warn)" : "var(--text-3)"
  return (
    <span className="mono tnum text-[12px]" style={{ color }}>
      {score.toFixed(2)}
    </span>
  )
}

/**
 * FindingsTable renders one page of FindingRow items with the Security view's
 * columns, the loading skeleton, and the empty state. It is the reusable surface
 * shared by the top-level Security page and the session view's Security tab, so
 * neither forks a near-duplicate table.
 *
 * Each row links to BOTH the message it came from (the whole row, and the
 * Message action, open the inspector for the finding's correlation id via
 * onOpenMessage) and the session it came from (the Session link pivots to the
 * session view via onOpenSession). Rows stay lean: the finding, its time, and
 * where it came from.
 */
export function FindingsTable({
  findings,
  status,
  limit,
  emptyText,
  onOpenMessage,
  onOpenSession,
  showSession = true,
}: {
  findings: FindingRow[]
  status: "loading" | "ok" | "error"
  limit: number
  emptyText: string
  // onOpenMessage opens the message inspector for a finding's source request
  // (its correlation id) — fired by a row click/Enter and by the Message action.
  onOpenMessage: (finding: FindingRow, index: number) => void
  // onOpenSession pivots to the session a finding came from (its session id).
  onOpenSession: (sessionID: string) => void
  // showSession renders the Session link column. The top-level page shows it
  // (rows span sessions); the session-scoped tab hides it — every row is already
  // this session, so the link would only point back at the current view.
  showSession?: boolean
}) {
  const cols = showSession ? 9 : 8
  return (
    <TableScroll>
      <thead>
        <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
          <th scope="col" className="text-left font-medium px-4 py-2">Time</th>
          <th scope="col" className="text-left font-medium px-4 py-2">Check</th>
          <th scope="col" className="text-left font-medium px-4 py-2">Category</th>
          <th scope="col" className="text-left font-medium px-4 py-2">Offending text</th>
          <th scope="col" className="text-left font-medium px-4 py-2">Unit</th>
          <th scope="col" className="text-right font-medium px-4 py-2">Score</th>
          <th scope="col" className="text-left font-medium px-4 py-2">Model</th>
          <th scope="col" className="text-left font-medium px-4 py-2">Configuration</th>
          {showSession && <th scope="col" className="text-left font-medium px-4 py-2">Session</th>}
        </tr>
      </thead>
      <tbody aria-busy={status === "loading"}>
        {status === "loading" && (
          <SkeletonRows
            rows={Math.min(limit, 12)}
            cols={[
              { w: "9rem" },
              { w: "4.5rem" },
              { w: "7rem" },
              { w: "12rem" },
              { w: "4rem" },
              { w: "2.5rem", align: "right" as const },
              { w: "5rem" },
              { w: "5rem" },
              ...(showSession ? [{ w: "6rem" }] : []),
            ]}
          />
        )}
        {status !== "loading" &&
          findings.map((f, i) => {
            const unit = [f.unit_role, f.unit_kind].filter(Boolean).join(" · ")
            return (
              <tr
                key={`${f.correlation_id}:${f.category}:${i}`}
                role="button"
                tabIndex={0}
                aria-label={`Open message for ${f.check_type} finding ${f.category}`}
                onClick={() => onOpenMessage(f, i)}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter" || ev.key === " ") {
                    ev.preventDefault()
                    onOpenMessage(f, i)
                  }
                }}
                className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[color:var(--accent)]"
              >
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap">
                  {f.observed_at ? fmt.fullTime(f.observed_at) : <Dash />}
                </td>
                <td className="px-4 py-2"><CheckTypeBadge checkType={f.check_type} /></td>
                <td className="mono text-[12px] px-4 py-2 text-[color:var(--text-2)]">{f.category || <Dash />}</td>
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-2)] max-w-[18rem]">
                  {f.offending_text ? (
                    <span className="block truncate" title={f.offending_text}>{f.offending_text}</span>
                  ) : (
                    <Dash />
                  )}
                </td>
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)]">{unit || <Dash />}</td>
                <td className="text-right px-4 py-2"><ScoreCell score={f.score} /></td>
                <td className="mono text-[12px] px-4 py-2">{f.model || <Dash />}</td>
                <td className="mono text-[12px] px-4 py-2">{f.configuration || <Dash />}</td>
                {showSession && (
                  <td className="mono text-[11.5px] px-4 py-2 max-w-[12rem]">
                    {f.session_id ? (
                      <button
                        type="button"
                        // Stop the row's message-open: this link pivots to the
                        // session instead of opening the request inspector.
                        onClick={(ev) => {
                          ev.stopPropagation()
                          onOpenSession(f.session_id!)
                        }}
                        title={`View session ${f.session_id}`}
                        className="inline-flex max-w-full items-center gap-1 truncate text-left text-[color:var(--accent)] hover:underline focus-visible:outline-2 focus-visible:outline-[color:var(--accent)]"
                      >
                        <span className="truncate">{f.session_id}</span>
                        <ArrowUpRight size={12} className="shrink-0" />
                      </button>
                    ) : (
                      <Dash />
                    )}
                  </td>
                )}
              </tr>
            )
          })}
        {status === "ok" && findings.length === 0 && (
          <tr>
            <td colSpan={cols} className="px-4 py-10 text-center text-[12px] text-[color:var(--text-4)]">
              {emptyText}
            </td>
          </tr>
        )}
      </tbody>
    </TableScroll>
  )
}
