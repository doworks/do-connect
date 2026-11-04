import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'
import { contactsService, messagesService } from '@/services/api'
import { phoneDigits } from '@/lib/phone'
import { insertionIndex, laterOf, opensWindow } from '@/lib/messageTime'
import { applyMessageToContact, createAgentTyping, releaseHeldMessage, type ContactExtras, type ListMessage, type MessageExtras } from './contactExtras'

// Phones are stored without leading + or whitespace (see CreateContact in
// internal/handlers/contacts.go). Strip them from a digit-only query so a user
// typing "+91 98765 43210" still matches a stored "919876543210" via the
// server's substring LIKE.
function normalizeContactSearch(raw: string): string {
  return phoneDigits(raw) || raw.trim().replace(/^\+/, '')
}

export interface Contact extends ContactExtras {
  id: string
  phone_number: string
  name: string
  profile_name?: string
  avatar_url?: string
  status: string
  tags: string[]
  metadata: Record<string, any>
  last_message_at?: string
  last_inbound_at?: string
  service_window_open?: boolean
  unread_count: number
  assigned_user_id?: string
  whatsapp_account?: string
  marketing_opt_out?: boolean
  created_at: string
  updated_at: string
}

export interface ReplyPreview {
  id: string
  content: any
  message_type: string
  direction: 'incoming' | 'outgoing'
}

export interface Reaction {
  emoji: string
  from_phone?: string
  from_user?: string
}

export interface Message extends MessageExtras {
  id: string
  contact_id: string
  direction: 'incoming' | 'outgoing'
  message_type: string
  content: any
  media_url?: string
  media_mime_type?: string
  media_filename?: string
  interactive_data?: {
    type?: string
    body?: string
    buttons?: Array<{
      type?: string
      reply?: { id: string; title: string }
      id?: string
      title?: string
    }>
    rows?: Array<{
      id?: string
      title?: string
    }>
  }
  status: string
  wamid?: string
  error_message?: string
  is_reply?: boolean
  reply_to_message_id?: string
  reply_to_message?: ReplyPreview
  reactions?: Reaction[]
  whatsapp_account?: string
  created_at: string
  updated_at: string
}

const CONTACTS_REFRESH_DELAY_MS = 3000

export const useContactsStore = defineStore('contacts', () => {
  const contacts = ref<Contact[]>([])
  const currentContact = ref<Contact | null>(null)
  const messages = ref<Message[]>([])
  // The message that came in live most recently, which a late one may have put
  // above the end of the chat.
  const lastAddedId = ref<string | null>(null)
  const isLoading = ref(false)
  const isLoadingMessages = ref(false)
  const isLoadingOlderMessages = ref(false)
  const hasMoreMessages = ref(false)
  const searchQuery = ref('')
  const selectedTags = ref<string[]>([])
  const replyingTo = ref<Message | null>(null)
  const accountFilter = ref<string | null>(null)

  // Contacts pagination
  const contactsPage = ref(1)
  const contactsLimit = ref(50)
  const contactsTotal = ref(0)
  const isLoadingMoreContacts = ref(false)
  const hasMoreContacts = computed(() => contacts.value.length < contactsTotal.value)

  // Search is now driven server-side via fetchContacts({ search }), so the
  // visible list is whatever the server returned — no extra local filtering.
  const filteredContacts = computed(() => contacts.value)

  const sortedContacts = computed(() => {
    return [...filteredContacts.value].sort((a, b) => {
      const dateA = a.last_message_at ? new Date(a.last_message_at).getTime() : 0
      const dateB = b.last_message_at ? new Date(b.last_message_at).getTime() : 0
      return dateB - dateA
    })
  })

  let latestMessagesRequest = ''

  const { agentTyping, noteAgentTyping } = createAgentTyping(currentContact)

  const listFilter = ref('all')
  const filterCounts = ref<Record<string, number>>({})

  async function fetchContacts(params?: { search?: string; page?: number; limit?: number; tags?: string }) {
    isLoading.value = true
    try {
      const tagsParam = selectedTags.value.length > 0 ? selectedTags.value.join(',') : undefined
      // Keep the active search, so a background refresh doesn't clobber
      // the list the agent is searching.
      const search = normalizeContactSearch(searchQuery.value) || undefined
      const response = await contactsService.list({
        page: 1,
        limit: contactsLimit.value,
        tags: tagsParam,
        search,
        filter: listFilter.value === 'all' ? undefined : listFilter.value,
        ...params
      })
      // API returns { status: "success", data: { contacts: [...], total: number } }
      const data = response.data.data || response.data
      contacts.value = data.contacts || []
      contactsTotal.value = data.total ?? contacts.value.length
      contactsPage.value = 1
      filterCounts.value = data.filter_counts || {}
    } catch (error) {
      console.error('Failed to fetch contacts:', error)
    } finally {
      isLoading.value = false
    }
  }

  async function loadMoreContacts() {
    if (isLoadingMoreContacts.value || !hasMoreContacts.value) return

    isLoadingMoreContacts.value = true
    try {
      const nextPage = contactsPage.value + 1
      const tagsParam = selectedTags.value.length > 0 ? selectedTags.value.join(',') : undefined
      const search = normalizeContactSearch(searchQuery.value) || undefined
      const response = await contactsService.list({
        page: nextPage,
        limit: contactsLimit.value,
        tags: tagsParam,
        search
      })
      const data = response.data.data || response.data
      const newContacts = data.contacts || []

      if (newContacts.length > 0) {
        // Append new contacts, avoiding duplicates
        const existingIds = new Set(contacts.value.map(c => c.id))
        const uniqueNew = newContacts.filter((c: Contact) => !existingIds.has(c.id))
        contacts.value = [...contacts.value, ...uniqueNew]
        contactsPage.value = nextPage
      }
      contactsTotal.value = data.total ?? contactsTotal.value
    } catch (error) {
      console.error('Failed to load more contacts:', error)
    } finally {
      isLoadingMoreContacts.value = false
    }
  }

  async function fetchContact(id: string) {
    try {
      const response = await contactsService.get(id)
      // API returns { status: "success", data: { ... } }
      const data = response.data.data || response.data
      currentContact.value = data
      return data
    } catch (error) {
      console.error('Failed to fetch contact:', error)
      return null
    }
  }

  async function fetchMessages(contactId: string, params?: { page?: number; limit?: number; account?: string }) {
    isLoadingMessages.value = true
    const requestedContact = contactId
    latestMessagesRequest = requestedContact
    // Drop the previous contact's messages immediately so the list doesn't show
    // stale content under the new contact's header while the fetch is in flight.
    messages.value = []
    lastAddedId.value = null
    try {
      const response = await messagesService.list(contactId, params)
      // API returns { status: "success", data: { messages: [...], has_more: boolean } }
      // A slow answer for a chat staff have already left must not land in this one.
      if (latestMessagesRequest !== requestedContact) return
      const data = response.data.data || response.data
      messages.value = data.messages || []
      hasMoreMessages.value = data.has_more === true
    } catch (error) {
      console.error('Failed to fetch messages:', error)
    } finally {
      if (latestMessagesRequest === requestedContact) isLoadingMessages.value = false
    }
  }

  async function fetchOlderMessages(contactId: string, account?: string) {
    if (isLoadingOlderMessages.value || !hasMoreMessages.value || messages.value.length === 0) {
      return
    }

    isLoadingOlderMessages.value = true
    try {
      // Get the oldest message ID for cursor-based pagination
      const oldestMessageId = messages.value[0].id
      const response = await messagesService.list(contactId, { before_id: oldestMessageId, account })
      const data = response.data.data || response.data
      const olderMessages = data.messages || []

      if (olderMessages.length > 0) {
        // Prepend older messages (they come in chronological order, oldest first)
        lastAddedId.value = null
        messages.value = [...olderMessages, ...messages.value]
      }
      hasMoreMessages.value = data.has_more === true
    } catch (error) {
      console.error('Failed to fetch older messages:', error)
    } finally {
      isLoadingOlderMessages.value = false
    }
  }

  async function sendMessage(
    contactId: string,
    type: string,
    content: any,
    replyToMessageId?: string,
    whatsappAccount?: string,
    extra?: { interactive?: Parameters<typeof messagesService.send>[1]['interactive'] },
  ) {
    try {
      const response = await messagesService.send(contactId, {
        type,
        content,
        reply_to_message_id: replyToMessageId,
        whatsapp_account: whatsappAccount,
        ...(extra?.interactive ? { interactive: extra.interactive } : {}),
      })
      // API returns { status: "success", data: { ... } }
      const newMessage = response.data.data || response.data
      // Use addMessage which has duplicate checking (WebSocket may also broadcast this)
      if (currentContact.value?.id === contactId) addMessage(newMessage)

      return newMessage
    } catch (error) {
      console.error('Failed to send message:', error)
      throw error
    }
  }

  async function sendTemplate(
    contactId: string,
    templateName: string,
    templateParams?: Record<string, string>,
    accountName?: string,
    headerFile?: File,
    buttonParams?: Record<string, string>,
    headerParams?: Record<string, string>
  ) {
    try {
      const response = await messagesService.sendTemplate(contactId, {
        template_name: templateName,
        template_params: templateParams,
        header_params: headerParams,
        button_params: buttonParams,
        account_name: accountName
      }, headerFile)
      const data = response.data.data || response.data
      // Use addMessage which has duplicate checking (WebSocket may also broadcast this)
      addMessage(data)
      return data
    } catch (error) {
      console.error('Failed to send template:', error)
      throw error
    }
  }

  function setReplyingTo(message: Message | null) {
    replyingTo.value = message
  }

  function clearReplyingTo() {
    replyingTo.value = null
  }

  // Reflect a new message on its sidebar row. Returns false when the contact
  // isn't in the loaded list (a new conversation, or beyond the loaded pages).
  function updateContactFromMessage(message: ListMessage): boolean {
    const contact = contacts.value.find(c => c.id === message.contact_id)
    if (!contact) return false
    applyMessageToContact(contact, message)
    return true
  }

  function markContactRead(contactId: string) {
    const contact = contacts.value.find(c => c.id === contactId)
    if (contact) {
      contact.unread_count = 0
      contact.marked_unread = false
    }
  }

  function markContactUnread(contactId: string) {
    const contact = contacts.value.find(c => c.id === contactId)
    if (contact) {
      contact.unread_count = Math.max(contact.unread_count, 1)
      contact.marked_unread = true
    }
  }

  function removeMessage(messageId: string) {
    messages.value = messages.value.filter(m => m.id !== messageId)
  }

  function setListFilter(filter: string) {
    listFilter.value = filter
    fetchContacts()
  }

  // Coalesce refetches triggered by live events, so a busy inbox doesn't turn
  // every message into a GET /contacts from every connected agent.
  let refreshHandle: ReturnType<typeof setTimeout> | null = null
  function scheduleContactsRefresh() {
    if (refreshHandle) return
    refreshHandle = setTimeout(() => {
      refreshHandle = null
      fetchContacts()
    }, CONTACTS_REFRESH_DELAY_MS)
  }

  function addMessage(message: Message) {
    // Update contact metadata regardless of account filter
    updateContactFromMessage(message)
    // Also update currentContact if it matches
    if (currentContact.value && currentContact.value.id === message.contact_id && message.direction === 'incoming') {
      currentContact.value.last_inbound_at = laterOf(currentContact.value.last_inbound_at, message.created_at)
      if (opensWindow(message)) currentContact.value.service_window_open = true
    }

    // Skip adding to messages array if account filter is active and doesn't match
    if (accountFilter.value && message.whatsapp_account && message.whatsapp_account !== accountFilter.value) {
      return
    }

    // Check if message already exists
    const exists = messages.value.some(m => m.id === message.id)
    if (exists) return
    // In time order, so a late message from the patient sits where they wrote it.
    // One older than everything loaded shows once older messages are loaded;
    // putting it first now would make the next page skip what lies between.
    const index = insertionIndex(messages.value, message.created_at)
    if (index === 0 && hasMoreMessages.value && messages.value.length > 0) return
    messages.value.splice(index, 0, message)
    lastAddedId.value = message.id
  }

  function updateMessageStatus(messageId: string, status: string, errorMessage?: string) {
    // A held message someone cancelled.
    if (status === 'cancelled') {
      removeMessage(messageId)
      return
    }
    const index = messages.value.findIndex(m => m.id === messageId)
    if (index !== -1) {
      const updated = {
        ...messages.value[index],
        status,
        ...(errorMessage ? { error_message: errorMessage } : {})
      }
      // A held message going out moves to where the patient actually gets it.
      if (messages.value[index].status === 'held' && status !== 'failed') return releaseHeldMessage(messages.value, index, updated, contacts.value)
      messages.value[index] = updated
    }
  }

  function setCurrentContact(contact: Contact | null) {
    currentContact.value = contact
    replyingTo.value = null // Clear reply state when switching contacts
    if (contact) {
      contact.unread_count = 0
    }
  }

  function setAccountFilter(account: string | null) {
    accountFilter.value = account
  }

  function clearMessages() {
    messages.value = []
    lastAddedId.value = null
    hasMoreMessages.value = false
    accountFilter.value = null
  }

  function updateMessageReactions(messageId: string, reactions: Reaction[]) {
    const message = messages.value.find(m => m.id === messageId)
    if (message) {
      message.reactions = reactions
    }
  }

  function updateContactTags(contactId: string, tags: string[]) {
    // Update in contacts list
    const contact = contacts.value.find(c => c.id === contactId)
    if (contact) {
      contact.tags = tags
    }
    // Update current contact if it matches
    if (currentContact.value?.id === contactId) {
      currentContact.value = { ...currentContact.value, tags }
    }
  }

  // Debounce server-side search so each keystroke doesn't fire a request.
  let searchDebounceHandle: ReturnType<typeof setTimeout> | null = null
  watch(searchQuery, (query) => {
    if (searchDebounceHandle) clearTimeout(searchDebounceHandle)
    searchDebounceHandle = setTimeout(() => {
      const search = normalizeContactSearch(query) || undefined
      fetchContacts({ search })
    }, 300)
  })

  return {
    contacts,
    currentContact,
    messages,
    lastAddedId,
    isLoading,
    isLoadingMessages,
    isLoadingOlderMessages,
    hasMoreMessages,
    searchQuery,
    selectedTags,
    replyingTo,
    filteredContacts,
    sortedContacts,
    // Contacts pagination
    contactsTotal,
    hasMoreContacts,
    isLoadingMoreContacts,
    fetchContacts,
    loadMoreContacts,
    updateContactFromMessage,
    markContactRead,
    scheduleContactsRefresh,
    // Other
    fetchContact,
    fetchMessages,
    fetchOlderMessages,
    sendMessage,
    listFilter,
    filterCounts,
    setListFilter,
    markContactUnread,
    removeMessage,
    agentTyping,
    noteAgentTyping,
    sendTemplate,
    addMessage,
    updateMessageStatus,
    setCurrentContact,
    clearMessages,
    setAccountFilter,
    setReplyingTo,
    clearReplyingTo,
    updateMessageReactions,
    updateContactTags
  }
})
