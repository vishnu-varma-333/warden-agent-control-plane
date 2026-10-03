"use server";

import { revalidatePath } from "next/cache";
import { controlApi } from "@/lib/api";

export async function decideApproval(id: string, state: "approved" | "rejected") {
  await controlApi.post(`/approvals/${id}/decide`, { state, decidedBy: "console-admin" });
  revalidatePath("/approvals");
}
