// A patient's message can reach us late (WhatsApp retries, replays). It shows at
// the time they sent it, so a chat stays in the order things were written, and
// a late message never moves a chat back in time.

// More than this between sending and arriving and the message info shows both.
export const LATE_ARRIVAL_MS = 2 * 60 * 1000
const SERVICE_WINDOW_MS = 24 * 60 * 60 * 1000

function toTime(value?: string | null): number {
  return value ? new Date(value).getTime() : Number.NaN
}

// Where a message goes in a chat ordered oldest first. Live messages are
// almost always the newest, so this looks from the end.
export function insertionIndex(list: ReadonlyArray<{ created_at: string }>, createdAt: string): number {
  const at = toTime(createdAt)
  if (Number.isNaN(at)) return list.length
  let index = list.length
  while (index > 0 && toTime(list[index - 1].created_at) > at) index--
  return index
}

export function isNewer(createdAt: string, current?: string | null): boolean {
  const previous = toTime(current)
  return Number.isNaN(previous) || toTime(createdAt) >= previous
}

export function laterOf(current: string | undefined | null, next: string): string {
  return current && !isNewer(next, current) ? current : next
}

// WhatsApp's 24 hours run from when the patient sent it, so a message that took
// longer than that to reach us doesn't open the chat. Only the server's times
// are compared, so a wrong clock here doesn't matter.
export function opensWindow(message: { created_at: string; received_at?: string }): boolean {
  if (!message.received_at) return true
  return toTime(message.received_at) - toTime(message.created_at) < SERVICE_WINDOW_MS
}

// When a patient's message reached us, if that was well after they sent it.
export function lateArrivalAt(message: { direction: string; created_at: string; received_at?: string }): string | null {
  if (message.direction !== 'incoming' || !message.received_at) return null
  return toTime(message.received_at) - toTime(message.created_at) > LATE_ARRIVAL_MS ? message.received_at : null
}
