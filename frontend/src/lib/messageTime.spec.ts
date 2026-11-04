import { describe, expect, it } from 'vitest'
import { insertionIndex, lateArrivalAt, laterOf, opensWindow } from './messageTime'

const at = (time: string) => ({ created_at: `2026-03-02T${time}:00Z` })

describe('insertionIndex', () => {
  const chat = [at('08:00'), at('09:00'), at('10:00')]

  it('puts a new message at the end', () => {
    expect(insertionIndex(chat, '2026-03-02T11:00:00Z')).toBe(3)
  })

  it('puts a late message where it was written', () => {
    expect(insertionIndex(chat, '2026-03-02T08:30:00Z')).toBe(1)
    expect(insertionIndex(chat, '2026-03-02T07:00:00Z')).toBe(0)
  })

  it('keeps arrival order for the same time, and the end for no time', () => {
    expect(insertionIndex(chat, '2026-03-02T09:00:00Z')).toBe(2)
    expect(insertionIndex(chat, '')).toBe(3)
  })
})

describe('laterOf', () => {
  it("doesn't move back in time", () => {
    expect(laterOf('2026-03-02T10:00:00Z', '2026-03-02T08:00:00Z')).toBe('2026-03-02T10:00:00Z')
    expect(laterOf('2026-03-02T10:00:00Z', '2026-03-02T11:00:00Z')).toBe('2026-03-02T11:00:00Z')
    expect(laterOf(undefined, '2026-03-02T08:00:00Z')).toBe('2026-03-02T08:00:00Z')
  })
})

describe('opensWindow', () => {
  it('counts 24 hours from when they sent it', () => {
    expect(opensWindow({ created_at: '2026-03-02T08:00:00Z' })).toBe(true)
    expect(opensWindow({ created_at: '2026-03-02T08:00:00Z', received_at: '2026-03-02T12:00:00Z' })).toBe(true)
    expect(opensWindow({ created_at: '2026-03-01T08:00:00Z', received_at: '2026-03-02T12:00:00Z' })).toBe(false)
  })
})

describe('lateArrivalAt', () => {
  const message = { direction: 'incoming', created_at: '2026-03-02T08:02:00Z' }

  it('shows when a late message arrived', () => {
    expect(lateArrivalAt({ ...message, received_at: '2026-03-02T12:03:00Z' })).toBe('2026-03-02T12:03:00Z')
  })

  it('stays quiet for one that came on time, or was ours', () => {
    expect(lateArrivalAt({ ...message, received_at: '2026-03-02T08:03:00Z' })).toBeNull()
    expect(lateArrivalAt(message)).toBeNull()
    expect(lateArrivalAt({ ...message, direction: 'outgoing', received_at: '2026-03-02T12:03:00Z' })).toBeNull()
  })
})
