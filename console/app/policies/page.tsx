import Link from "next/link";
import { Plus } from "lucide-react";
import { Badge } from "@/components/badge";
import { PageHeader } from "@/components/page-header";
import { controlApi } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { button, table } from "@/lib/ui";
import type { PolicyVersion } from "@/lib/types";
import { activatePolicy } from "./actions";

export const dynamic = "force-dynamic";

export default async function PoliciesPage() {
  const versions = await controlApi.get<PolicyVersion[]>("/policies");

  return (
    <div>
      <PageHeader
        title="Policy versions"
        description="Cedar policies evaluated on every model and tool call. Only one version is active at a time."
        actions={
          <Link href="/policies/new" className={button.primary}>
            <Plus size={14} strokeWidth={2.5} />
            New version
          </Link>
        }
      />

      <div className={table.wrap}>
        <table className="w-full text-left">
          <thead className={table.head}>
            <tr>
              <th className={table.headCell}>Version</th>
              <th className={table.headCell}>Status</th>
              <th className={table.headCell}>Created</th>
              <th className={table.headCell}></th>
            </tr>
          </thead>
          <tbody>
            {(versions ?? []).map((v) => (
              <tr key={v.version} className={table.row}>
                <td className={`${table.cell} font-medium`}>
                  <Link href={`/policies/${v.version}`} className="hover:text-accent hover:underline">
                    v{v.version}
                  </Link>
                </td>
                <td className={table.cell}>
                  {v.active ? <Badge tone="success">active</Badge> : <Badge>inactive</Badge>}
                </td>
                <td className={`${table.cell} tabular text-muted`}>{formatDateTime(v.createdAt)}</td>
                <td className={`${table.cell} text-right`}>
                  {!v.active && (
                    <form action={activatePolicy.bind(null, v.version)}>
                      <button type="submit" className={button.secondary}>
                        Activate
                      </button>
                    </form>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
