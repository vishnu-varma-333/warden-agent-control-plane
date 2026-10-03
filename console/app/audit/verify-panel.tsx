"use client";

import { useState, useTransition } from "react";
import { CheckCircle2, ShieldAlert } from "lucide-react";
import { button } from "@/lib/ui";
import { formatDuration } from "@/lib/format";
import type { VerifyResult } from "@/lib/types";
import { verifyChain } from "./actions";

export function VerifyPanel() {
  const [result, setResult] = useState<VerifyResult | null>(null);
  const [pending, startTransition] = useTransition();

  function onVerify() {
    startTransition(async () => {
      const r = await verifyChain();
      setResult(r);
    });
  }

  return (
    <div className="flex items-center gap-3 rounded-lg border border-border bg-surface px-4 py-3">
      <button onClick={onVerify} disabled={pending} className={button.secondary}>
        {pending ? "Verifying…" : "Verify chain"}
      </button>
      {result && (
        <div className="flex items-center gap-1.5 text-[13px]">
          {result.ok ? (
            <>
              <CheckCircle2 size={15} className="text-emerald-600" />
              <span className="text-foreground">
                {result.recordsVerified} record(s) verified clean in {formatDuration(result.durationNanos)}
              </span>
            </>
          ) : (
            <>
              <ShieldAlert size={15} className="text-red-600" />
              <span className="text-red-700">
                Tampered at seq {result.failureAt}: {result.failureReason}
              </span>
            </>
          )}
        </div>
      )}
    </div>
  );
}
