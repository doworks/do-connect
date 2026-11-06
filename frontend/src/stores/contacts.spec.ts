import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

vi.mock('@/services/api', () => ({ contactsService: {}, messagesService: {} }))

import { useContactsStore, type Contact } from './contacts'

function contact(id: string, name: string, tags: string[] = []): Contact {
  return { id, phone_number: '97300000001', name, profile_name: name, status: 'active', tags, metadata: {}, unread_count: 1 } as Contact
}

describe('applyContactUpdate', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('renames the chat in the list and in the open chat', () => {
    const store = useContactsStore()
    store.contacts = [contact('c1', 'Abu Omar'), contact('c2', 'Other')]
    store.currentContact = contact('c1', 'Abu Omar')

    store.applyContactUpdate({ contact_id: 'c1', name: 'EN · Omar Faisal +1', tags: ['Patient', 'Family · 2'] })

    expect(store.contacts[0].name).toBe('EN · Omar Faisal +1')
    expect(store.contacts[0].profile_name).toBe('EN · Omar Faisal +1')
    expect(store.contacts[0].tags).toEqual(['Patient', 'Family · 2'])
    expect(store.contacts[0].unread_count).toBe(1)
    expect(store.currentContact?.name).toBe('EN · Omar Faisal +1')
    expect(store.contacts[1].name).toBe('Other')
  })

  it('leaves what the update does not mention alone', () => {
    const store = useContactsStore()
    store.contacts = [contact('c1', 'Sara', ['VIP'])]
    store.applyContactUpdate({ contact_id: 'c1', tags: [] })
    expect(store.contacts[0].name).toBe('Sara')
    expect(store.contacts[0].tags).toEqual([])
  })

  it('ignores chats this screen has not loaded', () => {
    const store = useContactsStore()
    store.contacts = [contact('c1', 'Sara')]
    store.applyContactUpdate({ contact_id: 'zz', name: 'Someone' })
    expect(store.contacts).toHaveLength(1)
    expect(store.contacts[0].name).toBe('Sara')
  })
})
