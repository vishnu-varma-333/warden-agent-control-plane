// Server-only: this file must never be imported from a "use client"
// component. The admin token lives in a server-side env var and is
// attached here so it's never sent to the browser — the console's pages
// fetch through this helper (or a server action), not directly from
// client code.
import "server-only";

const BASE_URL = process.env.CONTROL_API_URL ?? "http://localhost:8081";
const ADMIN_TOKEN = process.env.ADMIN_TOKEN ?? "";

export class ControlApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${ADMIN_TOKEN}`,
      "Content-Type": "application/json",
      ...init?.headers,
    },
    cache: "no-store",
  });
  if (!res.ok) {
    const body = await res.text();
    throw new ControlApiError(res.status, body || res.statusText);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return res.json() as Promise<T>;
}

export const controlApi = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body ? JSON.stringify(body) : undefined }),
};
