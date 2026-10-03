import { Badge } from "@/components/badge";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { controlApi } from "@/lib/api";
import { formatCurrency } from "@/lib/format";
import { table } from "@/lib/ui";
import type { SpendScope } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function SpendPage() {
  const scopes = await controlApi.get<SpendScope[]>("/spend");

  return (
    <div>
      <PageHeader
        title="Spend"
        description="Budget consumption per scope for the current period. Enforced atomically in Redis — a rejected charge is never partially applied."
      />

      {!scopes || scopes.length === 0 ? (
        <EmptyState title="No spend recorded this period" />
      ) : (
        <div className={table.wrap}>
          <table className="w-full text-left">
            <thead className={table.head}>
              <tr>
                <th className={table.headCell}>Scope</th>
                <th className={table.headCell}>Usage</th>
                <th className={table.headCell}>Spent</th>
                <th className={table.headCell}>Limit</th>
              </tr>
            </thead>
            <tbody>
              {scopes.map((s) => {
                const pct = Math.min(100, (s.total / s.limit) * 100);
                const nearLimit = pct >= 80;
                return (
                  <tr key={s.scope} className={table.row}>
                    <td className={`${table.cell} font-medium`}>{s.scope}</td>
                    <td className={`${table.cell} w-56`}>
                      <div className="flex items-center gap-2">
                        <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-slate-100">
                          <div
                            className={`h-full rounded-full ${nearLimit ? "bg-amber-500" : "bg-accent"}`}
                            style={{ width: `${pct}%` }}
                          />
                        </div>
                        {nearLimit && <Badge tone="warning">{pct.toFixed(0)}%</Badge>}
                      </div>
                    </td>
                    <td className={`${table.cell} tabular text-muted`}>{formatCurrency(s.total)}</td>
                    <td className={`${table.cell} tabular text-muted`}>{formatCurrency(s.limit)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
