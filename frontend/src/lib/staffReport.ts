// Staff report: shapes of /analytics/staff and the small rules the view colours by.

export interface StaffDay {
  date: string
  chats_answered: number
  messages_sent: number
  first_replies: number
  on_target: number
  median_first_reply_secs: number | null
  p90_first_reply_secs: number | null
}

export interface StaffRow {
  user_id?: string
  name: string
  is_active: boolean
  is_available: boolean
  chats_answered: number
  messages_sent: number
  first_replies: number
  on_target: number
  median_first_reply_secs: number | null
  p90_first_reply_secs: number | null
  waiting_now: number
  oldest_waiting_since?: string
  owned_chats: number
  daily: StaffDay[]
}

export interface AutomaticSource {
  kind: 'labelled' | 'campaign' | 'other'
  label?: string
  messages_sent: number
  chats_reached: number
}

export interface StaffReport {
  from: string
  to: string
  timezone: string
  days: string[]
  target_secs: number
  slow_secs: number
  team: StaffRow
  people: StaffRow[]
  unassigned: StaffRow
  automatic: {
    messages_sent: number
    chats_reached: number
    sources: AutomaticSource[]
    daily: StaffDay[]
  }
}

/** good: within target, slow: up to the slow mark, very-slow: past it, none: nothing to judge. */
export type Tone = 'good' | 'slow' | 'very-slow' | 'none'

export function replyTone(secs: number | null | undefined, targetSecs: number, slowSecs: number): Tone {
  if (secs == null || Number.isNaN(secs)) return 'none'
  if (secs <= targetSecs) return 'good'
  if (secs <= slowSecs) return 'slow'
  return 'very-slow'
}

/** How long the oldest chat has waited, judged like a first reply would be. */
export function waitingSecs(since: string | undefined, now: Date): number | null {
  if (!since) return null
  const started = new Date(since).getTime()
  if (Number.isNaN(started)) return null
  return Math.max(0, (now.getTime() - started) / 1000)
}

/** Share of first replies within target, as a whole percent; null when there were none. */
export function onTargetPercent(row: { first_replies: number; on_target: number }): number | null {
  if (!row.first_replies) return null
  return Math.round((row.on_target / row.first_replies) * 100)
}

/** 80% or more on target is good, half or more is slow, below that very slow. */
export function shareTone(percent: number | null): Tone {
  if (percent == null) return 'none'
  if (percent >= 80) return 'good'
  if (percent >= 50) return 'slow'
  return 'very-slow'
}

/** 45s, 12m, 3h 5m, 2d 4h. */
export function formatDuration(secs: number | null | undefined): string {
  if (secs == null || Number.isNaN(secs)) return '—'
  const s = Math.max(0, Math.round(secs))
  if (s < 60) return `${s}s`
  const mins = Math.round(s / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) {
    const rest = mins % 60
    return rest ? `${hours}h ${rest}m` : `${hours}h`
  }
  const days = Math.floor(hours / 24)
  const restHours = hours % 24
  return restHours ? `${days}d ${restHours}h` : `${days}d`
}

export type SortKey =
  | 'name'
  | 'chats_answered'
  | 'messages_sent'
  | 'first_replies'
  | 'median_first_reply_secs'
  | 'p90_first_reply_secs'
  | 'on_target_percent'
  | 'waiting_now'
  | 'owned_chats'

function sortValue(row: StaffRow, key: SortKey): string | number | null {
  if (key === 'on_target_percent') return onTargetPercent(row)
  if (key === 'name') return row.name.toLowerCase()
  return row[key]
}

/** Sorts a copy. Rows without a value (no first replies yet) always go last. */
export function sortRows(rows: StaffRow[], key: SortKey, dir: 'asc' | 'desc'): StaffRow[] {
  const sign = dir === 'asc' ? 1 : -1
  return [...rows].sort((a, b) => {
    const va = sortValue(a, key)
    const vb = sortValue(b, key)
    if (va == null && vb == null) return a.name.localeCompare(b.name)
    if (va == null) return 1
    if (vb == null) return -1
    if (va < vb) return -sign
    if (va > vb) return sign
    return a.name.localeCompare(b.name)
  })
}

/** Whether a row has anything to show for the range or right now. */
export function hasActivity(row: StaffRow): boolean {
  return row.messages_sent > 0 || row.owned_chats > 0 || row.waiting_now > 0
}

/** Bar heights from 0 to 1 against the busiest day; a quiet range stays flat. */
export function barHeights(values: number[]): number[] {
  const max = Math.max(0, ...values)
  return values.map(v => (max > 0 ? v / max : 0))
}

/** A report day (YYYY-MM-DD, already in the clinic's zone) as a short label. */
export function dayLabel(date: string, locale?: string, withWeekday = false): string {
  const d = new Date(`${date}T00:00:00Z`)
  return d.toLocaleDateString(locale, {
    timeZone: 'UTC',
    month: 'short',
    day: 'numeric',
    ...(withWeekday ? { weekday: 'short' } : {}),
  })
}
