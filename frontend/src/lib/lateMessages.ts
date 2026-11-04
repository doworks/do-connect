// A patient's message that reached us late lands where they wrote it, which
// can be above the end of the open chat (see messageTime.ts).
import { nextTick } from 'vue'

interface HasId { id: string }

// The message that just came in, and whether it sits above the newest one.
export function latestArrival<T extends HasId>(messages: T[], lastAddedId: string | null): { latest: T; landedAbove: boolean } {
  const last = messages[messages.length - 1]
  const added = messages.find(m => m.id === lastAddedId)
  return { latest: added || last, landedAbove: !!added && added !== last }
}

// Whether message comes before the one with otherId. The unread line goes
// above the earliest new message, which a late one can be.
export function isBefore<T extends HasId>(messages: T[], message: T, otherId: string | null): boolean {
  return messages.indexOf(message) < messages.findIndex(m => m.id === otherId)
}

// Staff are looking at the chat, so show a late message rather than leave it up there unseen.
export function revealMessage(message: HasId) {
  nextTick(() => document.getElementById(`message-${message.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' }))
}
