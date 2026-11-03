import type { ClinicVisit } from '@/stores/clinic'

export function formatMoney(amount: number, currency: string): string {
  const digits = currency === 'BHD' ? 3 : 2
  const label = currency === 'BHD' ? 'BD' : currency
  return `${label} ${amount.toFixed(digits)}`
}

export function formatDay(date: string): string {
  return new Date(`${date}T00:00:00`).toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short' })
}

export function formatClock(time?: string): string {
  if (!time) return ''
  const [hours, minutes] = time.split(':').map(Number)
  const suffix = hours >= 12 ? 'PM' : 'AM'
  return `${hours % 12 || 12}:${String(minutes).padStart(2, '0')} ${suffix}`
}

export function formatVisitTime(visit: ClinicVisit): string {
  const clock = formatClock(visit.time)
  return clock ? `${formatDay(visit.date)}, ${clock}` : formatDay(visit.date)
}
