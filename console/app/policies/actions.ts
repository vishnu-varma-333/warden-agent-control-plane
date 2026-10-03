"use server";

import { revalidatePath } from "next/cache";
import { redirect } from "next/navigation";
import { controlApi, ControlApiError } from "@/lib/api";

export async function activatePolicy(version: number) {
  await controlApi.post(`/policies/${version}/activate`);
  revalidatePath("/policies");
}

export type CreatePolicyState = { error?: string };

export async function createPolicy(_prev: CreatePolicyState, formData: FormData): Promise<CreatePolicyState> {
  const cedarSource = String(formData.get("cedarSource") ?? "").trim();
  if (!cedarSource) {
    return { error: "Cedar source can't be empty." };
  }
  try {
    await controlApi.post<{ version: number }>("/policies", { cedarSource });
  } catch (err) {
    if (err instanceof ControlApiError) {
      return { error: err.message || "The control API rejected this policy." };
    }
    return { error: "Unexpected error creating the policy version." };
  }
  revalidatePath("/policies");
  redirect("/policies");
}
