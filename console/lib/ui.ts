// Small, shared Tailwind class strings so every button/badge in the
// console looks the same instead of being restyled ad hoc per page.
export const button = {
  primary:
    "inline-flex items-center justify-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-accent-foreground transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50",
  secondary:
    "inline-flex items-center justify-center gap-1.5 rounded-md border border-border bg-surface px-3 py-1.5 text-[13px] font-medium text-foreground transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50",
  danger:
    "inline-flex items-center justify-center gap-1.5 rounded-md border border-red-200 bg-red-50 px-3 py-1.5 text-[13px] font-medium text-red-700 transition-colors hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-50",
};

export const card = "rounded-lg border border-border bg-surface";

export const table = {
  wrap: "overflow-hidden rounded-lg border border-border bg-surface",
  head: "border-b border-border bg-slate-50/80 text-left text-[11.5px] font-semibold uppercase tracking-wide text-muted",
  headCell: "px-4 py-2.5",
  row: "border-b border-border last:border-0 hover:bg-slate-50/60",
  cell: "px-4 py-3 text-[13.5px] text-foreground",
};

export function badgeClass(tone: "neutral" | "success" | "warning" | "danger" | "info"): string {
  const base = "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11.5px] font-medium";
  switch (tone) {
    case "success":
      return `${base} bg-emerald-50 text-emerald-700`;
    case "warning":
      return `${base} bg-amber-50 text-amber-700`;
    case "danger":
      return `${base} bg-red-50 text-red-700`;
    case "info":
      return `${base} bg-indigo-50 text-indigo-700`;
    default:
      return `${base} bg-slate-100 text-slate-600`;
  }
}
