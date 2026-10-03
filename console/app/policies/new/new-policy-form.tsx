"use client";

import { useActionState, useState } from "react";
import Link from "next/link";
import { button } from "@/lib/ui";
import { createPolicy, type CreatePolicyState } from "../actions";

const PLACEHOLDER = `permit(
    principal,
    action == Warden::Action::"CallTool",
    resource == Warden::Tool::"echo"
);`;

export function NewPolicyForm() {
  const [state, formAction, pending] = useActionState<CreatePolicyState, FormData>(createPolicy, {});
  // Controlled, not just defaultValue: React resets uncontrolled form
  // fields after an action completes (including a failed one), which
  // would otherwise wipe what the admin just typed the moment validation
  // rejects it — exactly when they need it preserved to fix and resubmit.
  const [source, setSource] = useState("");

  return (
    <form action={formAction} className="space-y-4">
      <div>
        <label htmlFor="cedarSource" className="mb-1.5 block text-[13px] font-medium text-foreground">
          Cedar source
        </label>
        <textarea
          id="cedarSource"
          name="cedarSource"
          rows={16}
          required
          value={source}
          onChange={(e) => setSource(e.target.value)}
          placeholder={PLACEHOLDER}
          className="w-full rounded-lg border border-border bg-surface p-3 font-mono text-[12.5px] leading-relaxed text-foreground outline-none focus:border-accent focus:ring-1 focus:ring-accent"
        />
        <p className="mt-1.5 text-[12.5px] text-muted">
          Validated against Cedar&rsquo;s grammar on save. The new version is created inactive — activate it from
          the policies list once you&rsquo;re ready.
        </p>
      </div>

      {state.error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[13px] text-red-700">
          {state.error}
        </div>
      )}

      <div className="flex items-center gap-2">
        <button type="submit" disabled={pending} className={button.primary}>
          {pending ? "Validating…" : "Create version"}
        </button>
        <Link href="/policies" className={button.secondary}>
          Cancel
        </Link>
      </div>
    </form>
  );
}
