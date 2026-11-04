import { describe, expect, it } from 'vitest'
import {
  barHeights,
  dayLabel,
  formatDuration,
  hasActivity,
  onTargetPercent,
  replyTone,
  shareTone,
  sortRows,
  waitingSecs,
  type StaffRow,
} from './staffReport'

const row = (name: string, extra: Partial<StaffRow> = {}): StaffRow => ({
  name,
  is_active: true,
  is_available: true,
  chats_answered: 0,
  messages_sent: 0,
  first_replies: 0,
  on_target: 0,
  median_first_reply_secs: null,
  p90_first_reply_secs: null,
  waiting_now: 0,
  owned_chats: 0,
  daily: [],
  ...extra,
})

describe('replyTone', () => {
  it('splits at the target and the slow mark, inclusive', () => {
    expect(replyTone(900, 900, 3600)).toBe('good')
    expect(replyTone(901, 900, 3600)).toBe('slow')
    expect(replyTone(3600, 900, 3600)).toBe('slow')
    expect(replyTone(3601, 900, 3600)).toBe('very-slow')
  })

  it('has nothing to judge without a value', () => {
    expect(replyTone(null, 900, 3600)).toBe('none')
    expect(replyTone(undefined, 900, 3600)).toBe('none')
  })
})

describe('formatDuration', () => {
  it('uses the largest sensible unit', () => {
    expect(formatDuration(45)).toBe('45s')
    expect(formatDuration(180)).toBe('3m')
    expect(formatDuration(3600)).toBe('1h')
    expect(formatDuration(3900)).toBe('1h 5m')
    expect(formatDuration(86400 * 2 + 3600 * 4)).toBe('2d 4h')
    expect(formatDuration(null)).toBe('—')
  })
})

describe('waitingSecs', () => {
  it('measures from the start of the wait', () => {
    const now = new Date('2026-03-10T10:00:00Z')
    expect(waitingSecs('2026-03-10T09:30:00Z', now)).toBe(1800)
    expect(waitingSecs(undefined, now)).toBeNull()
    expect(waitingSecs('not a date', now)).toBeNull()
  })
})

describe('on-target share', () => {
  it('is a whole percent of first replies, and unknown without any', () => {
    expect(onTargetPercent({ first_replies: 3, on_target: 2 })).toBe(67)
    expect(onTargetPercent({ first_replies: 0, on_target: 0 })).toBeNull()
  })

  it('is good from 80%, slow from half', () => {
    expect(shareTone(80)).toBe('good')
    expect(shareTone(79)).toBe('slow')
    expect(shareTone(50)).toBe('slow')
    expect(shareTone(49)).toBe('very-slow')
    expect(shareTone(null)).toBe('none')
  })
})

describe('sortRows', () => {
  const rows = [
    row('Bob', { chats_answered: 3, median_first_reply_secs: 750 }),
    row('alice', { chats_answered: 5, median_first_reply_secs: 180 }),
    row('Zed'),
  ]

  it('sorts numbers either way and leaves missing values last', () => {
    expect(sortRows(rows, 'median_first_reply_secs', 'asc').map(r => r.name)).toEqual(['alice', 'Bob', 'Zed'])
    expect(sortRows(rows, 'median_first_reply_secs', 'desc').map(r => r.name)).toEqual(['Bob', 'alice', 'Zed'])
    expect(sortRows(rows, 'chats_answered', 'desc').map(r => r.name)).toEqual(['alice', 'Bob', 'Zed'])
  })

  it('sorts names without regard to case and leaves the input alone', () => {
    expect(sortRows(rows, 'name', 'asc').map(r => r.name)).toEqual(['alice', 'Bob', 'Zed'])
    expect(rows[0].name).toBe('Bob')
  })
})

describe('hasActivity', () => {
  it('counts sending, owning or leaving chats waiting', () => {
    expect(hasActivity(row('a'))).toBe(false)
    expect(hasActivity(row('a', { owned_chats: 1 }))).toBe(true)
    expect(hasActivity(row('a', { messages_sent: 2 }))).toBe(true)
  })
})

describe('barHeights', () => {
  it('scales to the busiest day and stays flat when quiet', () => {
    expect(barHeights([0, 2, 4])).toEqual([0, 0.5, 1])
    expect(barHeights([0, 0])).toEqual([0, 0])
    expect(barHeights([])).toEqual([])
  })
})

describe('dayLabel', () => {
  it('keeps the report day whatever the viewer zone', () => {
    expect(dayLabel('2026-03-01', 'en-US')).toBe('Mar 1')
    expect(dayLabel('2026-03-01', 'en-US', true)).toBe('Sun, Mar 1')
  })
})
