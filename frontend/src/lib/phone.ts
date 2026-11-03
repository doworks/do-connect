// Clinic staff type numbers in many shapes: "3311 2233", "+973 33112233",
// "0097333112233", or Arabic digits copied from a chat. Numbers are stored as
// digits with the country code and no "+".
const ARABIC_DIGITS = /[٠-٩۰-۹]/g

export function toLatinDigits(raw: string): string {
  return raw.replace(ARABIC_DIGITS, (d) => {
    const code = d.charCodeAt(0)
    return String(code >= 0x06f0 ? code - 0x06f0 : code - 0x0660)
  })
}

// Digits of a phone-looking query, with a leading + or 00 removed.
// Returns '' when the text isn't a phone number at all.
export function phoneDigits(raw: string): string {
  const latin = toLatinDigits(raw.trim())
  if (!latin || !/^[\d\s+()-]+$/.test(latin)) return ''
  return latin.replace(/[\s+()-]/g, '').replace(/^00/, '')
}

// Full international number for a query, or '' if it's too short to dial.
export function normalizePhone(raw: string, defaultCountryCode: string, localLength = 8): string {
  const digits = phoneDigits(raw)
  if (!digits) return ''
  if (defaultCountryCode && digits.length === localLength) return defaultCountryCode + digits
  return digits.length > localLength && digits.length <= 15 ? digits : ''
}

export function formatPhone(digits: string, defaultCountryCode: string): string {
  if (defaultCountryCode && digits.startsWith(defaultCountryCode)) {
    const local = digits.slice(defaultCountryCode.length)
    if (local.length === 8) return `+${defaultCountryCode} ${local.slice(0, 4)} ${local.slice(4)}`
    return `+${defaultCountryCode} ${local}`
  }
  return `+${digits}`
}
