import Link from "next/link";
import { Badge } from "@/components/badge";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { controlApi } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { button, table } from "@/lib/ui";
import type { ApprovalDetail } from "@/lib/types";
import { decideApproval } from "./actions";

export const dynamic = "force-dynamic";

const TABS = ["pending", "approved", "rejected", "expired", "all"] as const;

function stateTone(state: ApprovalDetail["state"]) {
  switch (state) {
    case "approved":
      return "success" as const;
    case "rejected":
      return "danger" as const;
    case "expired":
      return "warning" as const;
    default:
      return "info" as const;
  }
}

export default async function ApprovalsPage({
  searchParams,
}: {
  searchParams: Promise<{ state?: string }>;
}) {
  const { state: stateParam } = await searchParams;
  const activeTab = (TABS as readonly string[]).includes(stateParam ?? "") ? stateParam! : "pending";
  const query = activeTab === "all" ? "" : `?state=${activeTab}`;
  const approvals = await controlApi.get<ApprovalDetail[]>(`/approvals${query}`);

  return (
    <div>
      <PageHeader
        title="Approvals"
        description="Calls paused for a human decision. Survives a gateway restart — nothing here is lost or re-run twice."
      />

      <div className="mb-4 flex gap-1 border-b border-border">
        {TABS.map((tab) => (
          <Link
            key={tab}
            href={tab === "pending" ? "/approvals" : `/approvals?state=${tab}`}
            className={[
              "border-b-2 px-3 py-2 text-[13px] font-medium capitalize transition-colors",
              activeTab === tab
                ? "border-accent text-accent"
                : "border-transparent text-muted hover:text-foreground",
            ].join(" ")}
          >
            {tab}
          </Link>
        ))}
      </div>

      {!approvals || approvals.length === 0 ? (
        <EmptyState title={`No ${activeTab === "all" ? "" : activeTab} approvals`} />
      ) : (
        <div className={table.wrap}>
          <table className="w-full text-left">
            <thead className={table.head}>
              <tr>
                <th className={table.headCell}>Action</th>
                <th className={table.headCell}>Agent</th>
                <th className={table.headCell}>Acting as</th>
                <th className={table.headCell}>State</th>
                <th className={table.headCell}>Expires</th>
                <th className={table.headCell}></th>
              </tr>
            </thead>
            <tbody>
              {approvals.map((a) => (
                <tr key={a.id} className={table.row}>
                  <td className={table.cell}>
                    <div className="font-medium">{a.action}</div>
                    <div className="text-[12px] text-muted">
                      {a.resourceType}::{a.resourceId}
                    </div>
                  </td>
                  <td className={`${table.cell} text-muted`}>{a.agentId}</td>
                  <td className={`${table.cell} text-muted`}>{a.actingAs}</td>
                  <td className={table.cell}>
                    <Badge tone={stateTone(a.state)}>{a.state}</Badge>
                    {a.decidedBy && <div className="mt-0.5 text-[11.5px] text-muted">by {a.decidedBy}</div>}
                  </td>
                  <td className={`${table.cell} tabular text-muted`}>{formatDateTime(a.expiresAt)}</td>
                  <td className={`${table.cell} text-right`}>
                    {a.state === "pending" && (
                      <div className="flex justify-end gap-2">
                        <form action={decideApproval.bind(null, a.id, "rejected")}>
                          <button type="submit" className={button.danger}>
                            Reject
                          </button>
                        </form>
                        <form action={decideApproval.bind(null, a.id, "approved")}>
                          <button type="submit" className={button.primary}>
                            Approve
                          </button>
                        </form>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
