export type ToolRecord = {
  mcpServer: string;
  name: string;
  definitionHash: string;
  pendingHash?: string;
  status: "active" | "changed";
  updatedAt: string;
};

export type PolicyVersion = {
  version: number;
  cedarSource: string;
  active: boolean;
  createdAt: string;
};

export type ApprovalDetail = {
  id: string;
  agentId: string;
  actingAs: string;
  action: string;
  resourceType: string;
  resourceId: string;
  state: "pending" | "approved" | "rejected" | "expired";
  decidedBy?: string;
  executedAt?: string;
  expiresAt: string;
  createdAt: string;
};

export type AuditRecord = {
  seq: number;
  prevHash: string;
  hash: string;
  eventId: string;
  decision: string;
  reason: string;
  agentId: string;
  actingAs: string;
  action: string;
  resourceType: string;
  resourceId: string;
  payloadRef: string;
  occurredAt: string;
};

export type VerifyResult = {
  ok: boolean;
  recordsVerified: number;
  startSeq: number;
  failureAt: number;
  failureReason: string;
  durationNanos: number;
};

export type SpendScope = {
  scope: string;
  total: number;
  limit: number;
};
