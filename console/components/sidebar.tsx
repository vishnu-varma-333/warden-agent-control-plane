"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { ClipboardCheck, ScrollText, ShieldCheck, Wallet, Wrench } from "lucide-react";
import type { ComponentType } from "react";

const NAV: { href: string; label: string; icon: ComponentType<{ size?: number; strokeWidth?: number }> }[] = [
  { href: "/tools", label: "Tools", icon: Wrench },
  { href: "/policies", label: "Policies", icon: ShieldCheck },
  { href: "/approvals", label: "Approvals", icon: ClipboardCheck },
  { href: "/audit", label: "Audit log", icon: ScrollText },
  { href: "/spend", label: "Spend", icon: Wallet },
];

export function Sidebar() {
  const pathname = usePathname();

  return (
    <aside className="flex w-60 flex-col border-r border-sidebar-border bg-sidebar">
      <div className="flex h-14 items-center gap-2 border-b border-sidebar-border px-5">
        <div className="flex h-6 w-6 items-center justify-center rounded-md bg-accent text-[13px] font-semibold text-accent-foreground">
          W
        </div>
        <span className="text-[15px] font-semibold text-sidebar-foreground-active">Warden</span>
      </div>

      <nav className="flex-1 space-y-0.5 px-3 py-4">
        {NAV.map(({ href, label, icon: Icon }) => {
          const active = pathname === href || pathname.startsWith(href + "/");
          return (
            <Link
              key={href}
              href={href}
              className={[
                "flex items-center gap-2.5 rounded-md px-3 py-2 text-[13.5px] font-medium transition-colors",
                active
                  ? "bg-white/10 text-sidebar-foreground-active"
                  : "text-sidebar-foreground hover:bg-white/5 hover:text-sidebar-foreground-active",
              ].join(" ")}
            >
              <Icon size={16} strokeWidth={2} />
              {label}
            </Link>
          );
        })}
      </nav>

      <div className="border-t border-sidebar-border px-5 py-3">
        <p className="text-[11px] leading-snug text-sidebar-foreground/70">
          Agent control plane
          <br />
          Admin console
        </p>
      </div>
    </aside>
  );
}
