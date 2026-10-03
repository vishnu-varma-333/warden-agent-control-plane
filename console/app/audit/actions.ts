"use server";

import { controlApi } from "@/lib/api";
import type { VerifyResult } from "@/lib/types";

export async function verifyChain(): Promise<VerifyResult> {
  return controlApi.post<VerifyResult>("/audit/verify");
}
