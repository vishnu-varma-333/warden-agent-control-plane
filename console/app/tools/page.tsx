import { Badge } from "@/components/badge";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { controlApi } from "@/lib/api";
import { formatDateTime, truncateHash } from "@/lib/format";
import { button, table } from "@/lib/ui";
import type { ToolRecord } from "@/lib/types";
import { approveTool } from "./actions";

export const dynamic = "force-dynamic";

export default async function ToolsPage() {
  const tools = await controlApi.get<ToolRecord[]>("/tools");
  const sorted = (tools ?? []).slice().sort((a, b) => (a.status === b.status ? 0 : a.status === "changed" ? -1 : 1));

  return (
    <div>
      <PageHeader
        title="Tool registry"
        description="Every tool Warden has pinned from an upstream MCP server. A definition change is blocked until approved here."
      />

      {sorted.length === 0 ? (
        <EmptyState
          title="No tools registered yet"
          description="Tools appear here the first time an agent lists them through the MCP gateway."
        />
      ) : (
        <div className={table.wrap}>
          <table className="w-full text-left">
            <thead className={table.head}>
              <tr>
                <th className={table.headCell}>Server</th>
                <th className={table.headCell}>Tool</th>
                <th className={table.headCell}>Status</th>
                <th className={table.headCell}>Pinned hash</th>
                <th className={table.headCell}>Updated</th>
                <th className={table.headCell}></th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((t) => (
                <tr key={`${t.mcpServer}/${t.name}`} className={table.row}>
                  <td className={`${table.cell} text-muted`}>{t.mcpServer}</td>
                  <td className={`${table.cell} font-medium`}>{t.name}</td>
                  <td className={table.cell}>
                    {t.status === "changed" ? (
                      <Badge tone="danger">changed</Badge>
                    ) : (
                      <Badge tone="success">active</Badge>
                    )}
                  </td>
                  <td className={`${table.cell} font-mono text-[12px] text-muted`}>
                    {truncateHash(t.definitionHash)}
                    {t.pendingHash && (
                      <div className="mt-0.5 text-red-600">pending: {truncateHash(t.pendingHash)}</div>
                    )}
                  </td>
                  <td className={`${table.cell} tabular text-muted`}>{formatDateTime(t.updatedAt)}</td>
                  <td className={`${table.cell} text-right`}>
                    {t.status === "changed" && (
                      <form action={approveTool.bind(null, t.mcpServer, t.name)}>
                        <button type="submit" className={button.primary}>
                          Approve change
                        </button>
                      </form>
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
