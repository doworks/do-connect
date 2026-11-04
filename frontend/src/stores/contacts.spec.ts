import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

const listMessagesMock = vi.fn()
const sendMessageMock = vi.fn()

vi.mock('@/services/api', () => ({
  contactsService: {},
  messagesService: {
    list: (...args: unknown[]) => listMessagesMock(...args),
    send: (...args: unknown[]) => sendMessageMock(...args),
  },
}))

function deferred<T>() {
  let resolve: (value: T) => void = () => {}
  const promise = new Promise<T>(r => { resolve = r })
  return { promise, resolve }
}

function message(id: string, contactId: string) {
  return {
    id,
    contact_id: contactId,
    direction: 'outgoing',
    message_type: 'text',
    content: { body: id },
    status: 'sent',
    created_at: '2026-01-01T10:00:00Z',
    updated_at: '2026-01-01T10:00:00Z',
  }
}

function contact(id: string) {
  return { id, phone_number: id, name: id, status: 'active', tags: [], metadata: {}, unread_count: 0 }
}

beforeEach(() => {
  setActivePinia(createPinia())
  listMessagesMock.mockReset()
  sendMessageMock.mockReset()
})

describe('switching chats', () => {
  it('ignores a slow messages response for a chat the user already left', async () => {
    const { useContactsStore } = await import('./contacts')
    const store = useContactsStore()

    const first = deferred<unknown>()
    const second = deferred<unknown>()
    listMessagesMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)

    store.currentContact = contact('a') as any
    const loadingA = store.fetchMessages('a')
    store.currentContact = contact('b') as any
    const loadingB = store.fetchMessages('b')

    second.resolve({ data: { data: { messages: [message('b1', 'b')], has_more: false } } })
    await loadingB
    first.resolve({ data: { data: { messages: [message('a1', 'a')], has_more: true } } })
    await loadingA

    expect(store.messages.map(m => m.id)).toEqual(['b1'])
    expect(store.hasMoreMessages).toBe(false)
    expect(store.isLoadingMessages).toBe(false)
  })

  it('does not add a sent message to a different open chat', async () => {
    const { useContactsStore } = await import('./contacts')
    const store = useContactsStore()

    const sending = deferred<unknown>()
    sendMessageMock.mockReturnValueOnce(sending.promise)

    store.currentContact = contact('a') as any
    const send = store.sendMessage('a', 'text', { body: 'hello' })
    store.currentContact = contact('b') as any

    sending.resolve({ data: { data: message('a1', 'a') } })
    await send

    expect(store.messages).toHaveLength(0)
  })

  it('adds a sent message to the chat it was sent in', async () => {
    const { useContactsStore } = await import('./contacts')
    const store = useContactsStore()

    sendMessageMock.mockResolvedValueOnce({ data: { data: message('a1', 'a') } })
    store.currentContact = contact('a') as any
    await store.sendMessage('a', 'text', { body: 'hello' })

    expect(store.messages.map(m => m.id)).toEqual(['a1'])
  })
})
