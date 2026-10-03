import Link from "next/link";
import { Badge } from "@/components/badge";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { controlApi } from "@/lib/api";
import { formatDateTime, truncateHash } from "@/lib/format";
import { table } from "@/lib/ui";
import type { AuditRecord } from "@/lib/types";
import { VerifyPanel } from "./verify-panel";

export const dynamic = "force-dynamic";

export default async function AuditPage({
  searchParams,
}: {
  searchParams: Promise<{ before?: string }>;
}) {
  const { before } = await searchParams;
  const query = before ? `?limit=100&before=${before}` : "?limit=100";
  const records = await controlApi.get<AuditRecord[]>(`/audit${query}`);
  const oldest = records && records.length > 0 ? records[records.length - 1].seq : null;

  return (
    <div>
      <PageHeader
        title="Audit log"
        description="Every decision, hash-chained in order. Tampering with any record breaks every hash after it."
      />

      <div className="mb-4">
        <VerifyPanel />
      </div>

      {!records || records.length === 0 ? (
        <EmptyState title="No audit events yet" description="Decisions appear here once a policy evaluation runs." />
      ) : (
        <div className={table.wrap}>
          <table className="w-full text-left">
            <thead className={table.head}>
              <tr>
                <th className={table.headCell}>Seq</th>
                <th className={table.headCell}>Decision</th>
                <th className={table.headCell}>Agent / acting as</th>
                <th className={table.headCell}>Action</th>
                <th className={table.headCell}>Hash</th>
                <th className={table.headCell}>Occurred</th>
              </tr>
            </thead>
            <tbody>
              {records.map((r) => (
                <tr key={r.seq} className={table.row}>
                  <td className={`${table.cell} tabular text-muted`}>{r.seq}</td>
                  <td className={table.cell}>
                    <Badge tone={r.decision === "allow" ? "success" : "danger"}>{r.decision}</Badge>
                  </td>
                  <td className={table.cell}>
                    {r.agentId} <span className="text-muted">as</span> {r.actingAs}
                  </td>
                  <td className={table.cell}>
                    {r.action} <span className="text-muted">on</span> {r.resourceType}::{r.resourceId}
                  </td>
                  <td className={`${table.cell} font-mono text-[12px] text-muted`}>{truncateHash(r.hash)}</td>
                  <td className={`${table.cell} tabular text-muted`}>{formatDateTime(r.occurredAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {oldest !== null && records.length === 100 && (
            <div className="border-t border-border px-4 py-2.5">
              <Link href={`/audit?before=${oldest}`} className="text-[13px] font-medium text-accent hover:underline">
                Load older events →
              </Link>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
