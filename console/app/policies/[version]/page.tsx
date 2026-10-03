import Link from "next/link";
import { ArrowLeft } from "lucide-react";
import { notFound } from "next/navigation";
import { Badge } from "@/components/badge";
import { PageHeader } from "@/components/page-header";
import { controlApi } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { button } from "@/lib/ui";
import type { PolicyVersion } from "@/lib/types";
import { activatePolicy } from "../actions";

export const dynamic = "force-dynamic";

export default async function PolicyVersionPage({ params }: { params: Promise<{ version: string }> }) {
  const { version: versionParam } = await params;
  const version = Number(versionParam);
  const versions = await controlApi.get<PolicyVersion[]>("/policies");
  const policy = versions.find((v) => v.version === version);
  if (!policy) notFound();

  return (
    <div>
      <Link href="/policies" className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted hover:text-foreground">
        <ArrowLeft size={14} /> Back to policies
      </Link>

      <PageHeader
        title={`Policy v${policy.version}`}
        description={`Created ${formatDateTime(policy.createdAt)}`}
        actions={
          policy.active ? (
            <Badge tone="success">active</Badge>
          ) : (
            <form action={activatePolicy.bind(null, policy.version)}>
              <button type="submit" className={button.primary}>
                Activate this version
              </button>
            </form>
          )
        }
      />

      <div className="rounded-lg border border-border bg-surface">
        <div className="border-b border-border px-4 py-2.5 text-[11.5px] font-semibold uppercase tracking-wide text-muted">
          Cedar source
        </div>
        <pre className="overflow-x-auto p-4 font-mono text-[12.5px] leading-relaxed text-foreground">
          {policy.cedarSource}
        </pre>
      </div>
    </div>
  );
}
