import { describe, expect, it } from 'vitest'
import { CLINIC_DEFAULT, resolveSound, shouldSound, type ClinicSound } from './messageSound'

const clinic: ClinicSound = { ...CLINIC_DEFAULT }

describe('which sound plays', () => {
  it("follows the clinic until a person chooses", () => {
    expect(resolveSound(clinic, {})).toEqual({ sound: 'chime', volume: 60, when: 'always' })
    expect(resolveSound(clinic, { message_sound: 'pop', message_sound_volume: 20, message_sound_when: 'away' }))
      .toEqual({ sound: 'pop', volume: 20, when: 'away' })
    expect(resolveSound({ ...clinic, message_sound: 'bell', message_sound_volume: 30 }, { message_sound: '' }))
      .toEqual({ sound: 'bell', volume: 30, when: 'always' })
  })

  it('is silent when the clinic or the person turned it off, or the volume is 0', () => {
    expect(resolveSound({ ...clinic, message_sound_enabled: false }, { message_sound: 'pop' })).toBeNull()
    expect(resolveSound(clinic, { message_sound: 'off' })).toBeNull()
    expect(resolveSound(clinic, { message_sound_volume: 0 })).toBeNull()
    expect(resolveSound({ ...clinic, message_sound_volume: 0 }, {})).toBeNull()
  })

  it('keeps people who had turned alerts off silent, until they choose a sound', () => {
    expect(resolveSound(clinic, { new_message_alerts: false })).toBeNull()
    expect(resolveSound(clinic, { new_message_alerts: false, message_sound: 'drop' })?.sound).toBe('drop')
  })

  it('ignores a sound it does not know', () => {
    expect(resolveSound(clinic, { message_sound: 'siren' as any })?.sound).toBe('chime')
  })
})

describe('when it plays', () => {
  const now = 1_000_000
  const always = resolveSound(clinic, {})
  const away = resolveSound(clinic, { message_sound_when: 'away' })
  const looking = (contact: string | null) => ({ tab: 'x', at: now - 1000, contact })

  it('plays when nobody is in front of a tab', () => {
    expect(shouldSound(always, { presence: null, now, contactId: 'c1' })).toBe(true)
    expect(shouldSound(away, { presence: { tab: 'x', at: now - 60_000, contact: 'c1' }, now, contactId: 'c1' })).toBe(true)
    expect(shouldSound(away, { presence: { tab: 'x', at: 0, contact: 'c1' }, now, contactId: 'c1' })).toBe(true)
  })

  it('never for the chat someone is reading', () => {
    expect(shouldSound(always, { presence: looking('c1'), now, contactId: 'c1' })).toBe(false)
  })

  it('for other chats only when set to every message', () => {
    expect(shouldSound(always, { presence: looking('c2'), now, contactId: 'c1' })).toBe(true)
    expect(shouldSound(always, { presence: looking(null), now, contactId: 'c1' })).toBe(true)
    expect(shouldSound(away, { presence: looking('c2'), now, contactId: 'c1' })).toBe(false)
  })

  it('not at all when silent', () => {
    expect(shouldSound(null, { presence: null, now, contactId: 'c1' })).toBe(false)
  })
})
