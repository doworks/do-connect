// Product name and logo, set by the server in window.__BRAND__ (see
// internal/frontend/embed.go). Without one, the app shows the default name
// and its built-in logo.

export const DEFAULT_BRAND_NAME = 'Whatomate'

export interface Brand {
  name: string
  /** URL of the logo, or '' to use the built-in logo. */
  logo: string
}

export function readBrand(source: unknown): Brand {
  const raw = (source && typeof source === 'object' ? source : {}) as Record<string, unknown>
  const name = typeof raw.name === 'string' && raw.name.trim() ? raw.name.trim() : DEFAULT_BRAND_NAME
  const logo = typeof raw.logo === 'string' ? raw.logo : ''
  return { name, logo }
}

export const brand: Brand = readBrand(typeof window === 'undefined' ? undefined : (window as any).__BRAND__)

/** True when the operator has renamed the product. */
export const isRebranded = brand.name !== DEFAULT_BRAND_NAME

// vue-i18n treats these as message syntax; wrap them as literals.
function escapeMessageSyntax(text: string): string {
  return text.replace(/[{}@|]/g, ch => `{'${ch}'}`)
}

/**
 * Replaces the default product name in every string of the given locale
 * messages, in place, so translations need no per-brand copies.
 */
export function applyBrandName<T>(messages: T, name: string): T {
  if (!name || name === DEFAULT_BRAND_NAME) return messages
  const replacement = escapeMessageSyntax(name)
  const visit = (node: any) => {
    if (!node || typeof node !== 'object') return
    for (const key of Object.keys(node)) {
      const value = node[key]
      if (typeof value === 'string') {
        if (value.includes(DEFAULT_BRAND_NAME)) node[key] = value.split(DEFAULT_BRAND_NAME).join(replacement)
      } else {
        visit(value)
      }
    }
  }
  visit(messages)
  return messages
}
