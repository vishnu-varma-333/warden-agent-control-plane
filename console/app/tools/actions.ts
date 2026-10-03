"use server";

import { revalidatePath } from "next/cache";
import { controlApi } from "@/lib/api";

export async function approveTool(mcpServer: string, name: string) {
  await controlApi.post(`/tools/${encodeURIComponent(mcpServer)}/${encodeURIComponent(name)}/approve`);
  revalidatePath("/tools");
}
