// Date display helpers. The Go backend serializes an unset time.Time as
// "0001-01-01T00:00:00Z"; that must never reach the UI as a real date.

/** Locale-formatted date, or "—" when empty, invalid or year <= 1. */
export function formatLastUsed(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() <= 1) return '—';
  return d.toLocaleString();
}
