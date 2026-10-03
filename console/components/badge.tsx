import { badgeClass } from "@/lib/ui";

export function Badge({
  children,
  tone = "neutral",
}: {
  children: React.ReactNode;
  tone?: "neutral" | "success" | "warning" | "danger" | "info";
}) {
  return <span className={badgeClass(tone)}>{children}</span>;
}
