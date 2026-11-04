// When the clinic takes calls and which staff ring. The server applies the same
// rules when a call arrives (internal/callhours); this is so the settings page
// can show the result while someone is still editing. Change both together.

export interface TimeRange {
  start: string
  end: string
}

export interface DayHours {
  day: string
  enabled: boolean
  ranges: TimeRange[]
}

export interface ClosedDate {
  date: string
  label: string
}

export type RingMode = 'clinic' | 'shift' | 'always' | 'never'
export type DeviceMode = 'follow' | 'never'

export interface StaffDevice {
  id: string
  name: string
  kind: 'phone' | 'browser'
  mode: DeviceMode
  last_seen?: string
}

export interface StaffRing {
  id: string
  name: string
  role: string
  available: boolean
  mode: RingMode
  shift: DayHours[]
  devices: StaffDevice[]
}

export interface ByLanguage {
  en: string
  ar: string
}

export interface CallRules {
  // The organization's time zone, set under General settings.
  timezone: string
  closed_now: boolean
  hours: DayHours[]
  closed_dates: ClosedDate[]
  outside_action: 'message' | 'voice' | 'ring'
  message_gap_hours: number
  messages: { closed: ByLanguage; missed: ByLanguage }
  // Approved template names, for patients outside WhatsApp's 24-hour window.
  templates: { closed: ByLanguage; missed: ByLanguage }
  staff: StaffRing[]
}

export const WEEK = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday']

interface ClinicClock {
  day: string
  date: string
  time: string
}

export function clinicClock(at: Date, timezone: string): ClinicClock {
  const parts = new Intl.DateTimeFormat('en-GB', {
    timeZone: timezone,
    weekday: 'long',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(at)
  const get = (type: string) => parts.find(part => part.type === type)?.value || ''
  return {
    day: get('weekday').toLowerCase(),
    date: `${get('year')}-${get('month')}-${get('day')}`,
    time: `${get('hour')}:${get('minute')}`,
  }
}

function inHours(hours: DayHours[], clock: ClinicClock): boolean {
  const today = hours.find(entry => entry.day === clock.day)
  if (!today?.enabled) return false
  // The end time is exclusive: a range to 13:00 has stopped at 13:00.
  return today.ranges.some(range => clock.time >= range.start && clock.time < range.end)
}

export function isClinicOpen(rules: CallRules, at: Date): boolean {
  if (rules.closed_now) return false
  const clock = clinicClock(at, rules.timezone)
  if (rules.closed_dates.some(entry => entry.date === clock.date)) return false
  return inHours(rules.hours, clock)
}

function staffOnDuty(rules: CallRules, staff: StaffRing, at: Date): boolean {
  switch (staff.mode) {
    case 'always':
      return true
    case 'never':
      return false
    case 'shift':
      return inHours(staff.shift, clinicClock(at, rules.timezone))
    default:
      return isClinicOpen(rules, at) || rules.outside_action === 'ring'
  }
}

export function deviceRings(rules: CallRules, staff: StaffRing, device: StaffDevice, at: Date): boolean {
  if (rules.closed_now || !staff.available || device.mode === 'never') return false
  return staffOnDuty(rules, staff, at)
}

export function whoRings(rules: CallRules, at: Date): { staff: StaffRing; device: StaffDevice }[] {
  // Outside clinic hours only on-call staff ring, unless the clinic chose to ring as usual.
  const open = isClinicOpen(rules, at) || rules.outside_action === 'ring'
  const result: { staff: StaffRing; device: StaffDevice }[] = []
  for (const staff of rules.staff) {
    if (!open && staff.mode !== 'always') continue
    for (const device of staff.devices) {
      if (deviceRings(rules, staff, device, at)) result.push({ staff, device })
    }
  }
  return result
}

function clockLabel(time: string, arabic: boolean): string {
  const [hours, minutes] = time.split(':').map(Number)
  const clock = `${hours % 12 || 12}:${String(minutes).padStart(2, '0')}`
  if (arabic) return `${clock} ${hours >= 12 ? 'مساءً' : 'صباحاً'}`
  return `${clock} ${hours >= 12 ? 'PM' : 'AM'}`
}

const ARABIC_DAYS: Record<string, string> = {
  sunday: 'الأحد', monday: 'الاثنين', tuesday: 'الثلاثاء', wednesday: 'الأربعاء',
  thursday: 'الخميس', friday: 'الجمعة', saturday: 'السبت',
}

// "today at 4:00 PM", "tomorrow at 9:00 AM", "on Saturday at 9:00 AM".
export function nextOpenLabel(rules: CallRules, at: Date, language: 'en' | 'ar'): string {
  const arabic = language === 'ar'
  for (let offset = 0; offset < 14; offset++) {
    const clock = clinicClock(new Date(at.getTime() + offset * 86400000), rules.timezone)
    if (rules.closed_dates.some(entry => entry.date === clock.date)) continue
    const today = rules.hours.find(entry => entry.day === clock.day)
    if (!today?.enabled) continue
    const range = today.ranges.find(entry => offset > 0 || entry.start > clock.time)
    if (!range) continue
    const time = clockLabel(range.start, arabic)
    if (offset === 0) return arabic ? `اليوم الساعة ${time}` : `today at ${time}`
    if (offset === 1) return arabic ? `غداً الساعة ${time}` : `tomorrow at ${time}`
    const day = clock.day.charAt(0).toUpperCase() + clock.day.slice(1)
    return arabic ? `يوم ${ARABIC_DAYS[clock.day]} الساعة ${time}` : `on ${day} at ${time}`
  }
  return arabic ? 'قريباً' : 'soon'
}

export function hoursSummary(hours: DayHours[]): string {
  const open = hours.filter(entry => entry.enabled && entry.ranges.length)
  if (!open.length) return 'No hours set'
  const short = (day: string) => day.charAt(0).toUpperCase() + day.slice(1, 3)
  const label = (entry: DayHours) => entry.ranges.map(range => `${range.start}–${range.end}`).join(', ')
  const groups: { days: string[]; label: string }[] = []
  for (const entry of open) {
    const last = groups[groups.length - 1]
    // Only neighbouring days join up, so a closed Friday isn't read as open.
    const follows = last && WEEK.indexOf(entry.day) === WEEK.indexOf(last.days[last.days.length - 1]) + 1
    if (last && follows && last.label === label(entry)) last.days.push(entry.day)
    else groups.push({ days: [entry.day], label: label(entry) })
  }
  return groups
    .map(group => {
      const days = group.days.length > 2 ? `${short(group.days[0])}–${short(group.days[group.days.length - 1])}` : group.days.map(short).join(', ')
      return `${days} ${group.label}`
    })
    .join(' · ')
}
