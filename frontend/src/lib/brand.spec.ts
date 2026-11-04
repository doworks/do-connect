import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import { applyBrandName, brand, DEFAULT_BRAND_NAME, readBrand } from './brand'
import en from '@/i18n/locales/en.json'

describe('readBrand', () => {
  it('falls back to the default name and no logo', () => {
    expect(readBrand(undefined)).toEqual({ name: DEFAULT_BRAND_NAME, logo: '' })
    expect(readBrand({ name: '  ', logo: 42 })).toEqual({ name: DEFAULT_BRAND_NAME, logo: '' })
    expect(brand.name).toBe(DEFAULT_BRAND_NAME)
  })

  it('reads the configured name and logo', () => {
    expect(readBrand({ name: 'Acme Inbox', logo: '/favicon.svg?v=abc' }))
      .toEqual({ name: 'Acme Inbox', logo: '/favicon.svg?v=abc' })
  })
})

describe('applyBrandName', () => {
  it('replaces the default name in nested messages', () => {
    const messages = { en: { auth: { welcomeTitle: 'Welcome to Whatomate', other: 'Hi' } } }
    applyBrandName(messages, 'Acme Inbox')
    expect(messages.en.auth).toEqual({ welcomeTitle: 'Welcome to Acme Inbox', other: 'Hi' })
  })

  it('leaves messages untouched for the default name', () => {
    const messages = { en: { title: 'Welcome to Whatomate' } }
    applyBrandName(messages, DEFAULT_BRAND_NAME)
    expect(messages.en.title).toBe('Welcome to Whatomate')
  })

  it('brands every string of the real locale and renders through vue-i18n', () => {
    const messages = { en: structuredClone(en) }
    applyBrandName(messages, 'Acme {Inbox} @ Co | Ltd')
    expect(JSON.stringify(messages)).not.toContain(DEFAULT_BRAND_NAME)

    const i18n = createI18n({ legacy: false, locale: 'en', messages })
    expect(i18n.global.t('auth.welcomeTitle')).toBe('Welcome to Acme {Inbox} @ Co | Ltd')
  })
})
