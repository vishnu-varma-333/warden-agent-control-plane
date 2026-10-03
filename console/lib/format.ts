export function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function truncateHash(hash: string, length = 10): string {
  if (hash.length <= length * 2) return hash;
  return `${hash.slice(0, length)}…${hash.slice(-4)}`;
}

export function formatDuration(nanos: number): string {
  const ms = nanos / 1_000_000;
  if (ms < 1) return `${(nanos / 1000).toFixed(0)}µs`;
  if (ms < 1000) return `${ms.toFixed(1)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

export function formatCurrency(n: number): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}
