# Warden console

The admin UI behind control-api: tools, policies, approvals, audit and
spend. Next.js (App Router), TypeScript, Tailwind. Server Components fetch
from control-api directly; mutations (approve a tool, activate a policy,
decide an approval, verify the chain) go through Server Actions so the
admin token stays server-side and the browser never holds it.

## Running it

Needs control-api running first (see the root README). Then:

```bash
cp .env.local.example .env.local
# set ADMIN_TOKEN to the same value control-api is running with
npm install
npm run dev
```

Open http://localhost:3000 — it redirects to `/tools`.

## Why no login screen

control-api is gated by a single shared admin bearer token (see
`internal/httpapi/adminauth.go` and DECISIONS.md), not per-admin OAuth —
a deliberate v1 simplification for an operator-facing, single-tenant tool
meant to run behind a trusted network boundary, not the public internet.
The console holds that token only in a server-side env var (`ADMIN_TOKEN`,
never `NEXT_PUBLIC_*`) and uses it from Server Components/Actions, so it
never reaches client JavaScript. Anyone who can load the console can act
as admin; a real deployment would put it behind the same OAuth/OIDC flow
the gateway already enforces on agent traffic.

## Layout

- `lib/api.ts` — the one place that calls control-api, with the admin
  token attached. Imported only from server code (`server-only`).
- `lib/types.ts` — TypeScript mirrors of control-api's JSON responses.
- `app/<view>/page.tsx` — one Server Component per view, fetches and
  renders.
- `app/<view>/actions.ts` — Server Actions for that view's mutations,
  each followed by `revalidatePath` so the list reflects the change.
