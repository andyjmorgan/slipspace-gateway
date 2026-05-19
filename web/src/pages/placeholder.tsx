import { Tag } from "@/components/atoms/tag"

export function PlaceholderPage({ title }: { title: string }) {
  return (
    <div>
      <div className="mb-6">
        <h1 className="text-[24px] font-semibold tracking-[-0.02em]">{title}</h1>
        <div className="text-[13px] text-[color:var(--text-3)] mt-1">
          Not yet wired in this scaffold.
        </div>
      </div>
      <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-12 grid place-items-center">
        <div className="flex flex-col items-center gap-3 text-center max-w-[420px]">
          <Tag variant="violet">v1.1 — coming soon</Tag>
          <p className="text-[13.5px] text-[color:var(--text-2)]">
            The CRUD screens for this section will land alongside the management API
            (<span className="mono">cmd/api</span>) in the v1.1 milestone. This page is a
            placeholder; the design is fully specified in the prototype handoff.
          </p>
        </div>
      </div>
    </div>
  )
}
