import { describe, expect, it } from 'vitest'
import { hoursSummary, isClinicOpen, nextOpenLabel, whoRings, type CallRules, type DayHours, type StaffRing } from './callHours'

const week = (days: string[], ranges: [string, string][]): DayHours[] =>
  ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday'].map(day => ({
    day,
    enabled: days.includes(day),
    ranges: days.includes(day) ? ranges.map(([start, end]) => ({ start, end })) : [],
  }))

const person = (id: string, mode: StaffRing['mode'], extra: Partial<StaffRing> = {}): StaffRing => ({
  id,
  name: id,
  role: '',
  available: true,
  mode,
  shift: [],
  devices: [{ id: `${id}-phone`, name: 'phone', kind: 'phone', mode: 'follow' }],
  ...extra,
})

const rules = (over: Partial<CallRules> = {}): CallRules => ({
  timezone: 'Asia/Bahrain',
  closed_now: false,
  hours: week(['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'saturday'], [['09:00', '13:00'], ['16:00', '20:00']]),
  closed_dates: [],
  outside_action: 'message',
  message_gap_hours: 6,
  messages: { closed: { en: '', ar: '' }, missed: { en: '', ar: '' } },
  templates: { closed: { en: '', ar: '' }, missed: { en: '', ar: '' } },
  staff: [],
  ...over,
})

// internal/callhours/callhours_test.go checks the server against these cases.

// Tuesday 3 Nov 2026 in Bahrain
const at = (time: string) => new Date(`2026-11-03T${time}:00+03:00`)
const ids = (r: CallRules, when: Date) => whoRings(r, when).map(ring => ring.staff.id)

describe('clinic hours', () => {
  it('follows both daily periods in the clinic time zone', () => {
    expect(isClinicOpen(rules(), at('10:00'))).toBe(true)
    expect(isClinicOpen(rules(), at('14:30'))).toBe(false)
    expect(isClinicOpen(rules(), at('16:00'))).toBe(true)
  })

  it('stops at the end time', () => {
    expect(isClinicOpen(rules(), at('12:59'))).toBe(true)
    expect(isClinicOpen(rules(), at('13:00'))).toBe(false)
  })

  it('is closed on closed days and closed dates', () => {
    expect(isClinicOpen(rules(), new Date('2026-11-06T10:00:00+03:00'))).toBe(false)
    expect(isClinicOpen(rules({ closed_dates: [{ date: '2026-11-03', label: 'Holiday' }] }), at('10:00'))).toBe(false)
  })
})

describe('who rings', () => {
  const staff = [
    person('clinic', 'clinic'),
    person('evening', 'shift', { shift: week(['tuesday'], [['16:00', '20:00']]) }),
    person('oncall', 'always'),
    person('never', 'never'),
  ]

  it('during clinic hours rings clinic staff and whoever is on shift', () => {
    expect(ids(rules({ staff }), at('10:00'))).toEqual(['clinic', 'oncall'])
    expect(ids(rules({ staff }), at('17:00'))).toEqual(['clinic', 'evening', 'oncall'])
  })

  it('outside clinic hours rings only on-call staff', () => {
    expect(ids(rules({ staff }), at('22:00'))).toEqual(['oncall'])
  })

  it('a shift outside clinic hours stays quiet', () => {
    const late = [person('late', 'shift', { shift: week(['tuesday'], [['21:00', '23:00']]) })]
    expect(ids(rules({ staff: late }), at('22:00'))).toEqual([])
  })

  it('close for everyone silences on-call staff too', () => {
    expect(ids(rules({ staff, closed_now: true }), at('10:00'))).toEqual([])
  })

  it('ring as usual still respects each shift', () => {
    expect(ids(rules({ staff, outside_action: 'ring' }), at('22:00'))).toEqual(['clinic', 'oncall'])
  })

  it('away people and silenced phones never ring', () => {
    const quiet = [
      person('away', 'always', { available: false }),
      person('muted', 'always', { devices: [{ id: 'm', name: 'phone', kind: 'phone', mode: 'never' }] }),
    ]
    expect(ids(rules({ staff: quiet }), at('10:00'))).toEqual([])
  })
})

describe('next opening', () => {
  it('finds the second period the same day', () => {
    expect(nextOpenLabel(rules(), at('14:00'), 'en')).toBe('today at 4:00 PM')
  })

  it('finds tomorrow after closing', () => {
    expect(nextOpenLabel(rules(), at('21:00'), 'en')).toBe('tomorrow at 9:00 AM')
  })

  it('skips the closed Friday, in Arabic too', () => {
    const thursdayNight = new Date('2026-11-05T21:00:00+03:00')
    expect(nextOpenLabel(rules(), thursdayNight, 'en')).toBe('on Saturday at 9:00 AM')
    expect(nextOpenLabel(rules(), thursdayNight, 'ar')).toBe('يوم السبت الساعة 9:00 صباحاً')
  })

  it('skips closed dates', () => {
    const r = rules({ closed_dates: [{ date: '2026-11-04', label: 'Holiday' }] })
    expect(nextOpenLabel(r, at('21:00'), 'en')).toBe('on Thursday at 9:00 AM')
  })
})

describe('schedule summary', () => {
  it('does not read a closed day as open', () => {
    expect(hoursSummary(rules().hours)).toBe('Sun–Thu 09:00–13:00, 16:00–20:00 · Sat 09:00–13:00, 16:00–20:00')
  })
})
