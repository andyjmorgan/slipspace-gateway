import { useNavigate } from "react-router"
import { cn } from "@/lib/utils"

// AgentLink renders the agent identity of a routing row — the conversation id
// (the agent, from X-Claude-Code-Agent-Id) the judge decided for. When the
// owning session is known it links to that session's view (the pivot from "this
// agent" to "its whole conversation", the same navigation the request inspector
// uses); otherwise it degrades to plain text, since a savings row or an older
// judgement may carry no session id.
export function AgentLink({
  conversationId,
  sessionId,
  className,
}: {
  conversationId: string
  sessionId?: string
  className?: string
}) {
  const nav = useNavigate()
  if (sessionId) {
    return (
      <button
        type="button"
        onClick={() => nav(`/sessions/${encodeURIComponent(sessionId)}`)}
        className={cn("mono text-left text-[color:var(--accent)] hover:underline truncate min-w-0", className)}
        title={`View session ${sessionId}`}
      >
        {conversationId}
      </button>
    )
  }
  return (
    <span className={cn("mono truncate min-w-0", className)} title={conversationId}>
      {conversationId}
    </span>
  )
}
