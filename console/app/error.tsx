"use client";

import { AlertTriangle } from "lucide-react";
import { button } from "@/lib/ui";

export default function Error({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border bg-surface px-6 py-16 text-center">
      <AlertTriangle size={20} className="mb-3 text-amber-600" />
      <p className="text-[13.5px] font-medium text-foreground">Couldn&rsquo;t reach control-api</p>
      <p className="mt-1 max-w-sm text-[13px] text-muted">
        {error.message || "The admin API didn't respond as expected. Check that it's running and that ADMIN_TOKEN matches."}
      </p>
      <button onClick={reset} className={`${button.secondary} mt-4`}>
        Try again
      </button>
    </div>
  );
}
