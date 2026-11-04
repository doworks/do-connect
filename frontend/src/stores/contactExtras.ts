// Our fork's additions to the contacts store (contacts.ts), kept here so that
// upstream file changes as little as possible.
import { ref, type Ref } from 'vue'
import { isNewer, laterOf, opensWindow } from '@/lib/messageTime'
import type { Contact, Message } from './contacts'

export interface ContactExtras {
  whatsapp_name?: string
  marked_unread?: boolean
  last_message_preview?: string
  last_message_direction?: 'incoming' | 'outgoing'
  last_reply_by?: string
  last_send_failed?: boolean
  waiting_since?: string
  assigned_user_name?: string
}

export interface MessageExtras {
  media_missing?: boolean
  // A patient's message that reached us late; created_at is when they sent it.
  received_at?: string
  forwarded?: boolean
  saved_to_clinic?: { patient: string; patient_name?: string; kind: string; by_name: string; at: string }
  sent_by_user_id?: string
  sent_by_name?: string
  sent_by_label?: string
  template_name?: string
  delivered_at?: string
  read_at?: string
}

// Who else is typing in the open chat. Cleared if no more typing arrives.
export function createAgentTyping(currentContact: Ref<Contact | null>) {
  const agentTyping = ref('')
  let agentTypingTimer: ReturnType<typeof setTimeout> | null = null
  function noteAgentTyping(payload: { contact_id: string; user_id: string; name: string }) {
    if (payload.contact_id !== currentContact.value?.id) return
    agentTyping.value = payload.name
    if (agentTypingTimer) clearTimeout(agentTypingTimer)
    agentTypingTimer = setTimeout(() => { agentTyping.value = '' }, 4000)
  }
  return { agentTyping, noteAgentTyping }
}

export type ListMessage = Pick<Message, 'contact_id' | 'direction' | 'status' | 'created_at' | 'received_at' | 'message_type' | 'content' | 'sent_by_name' | 'sent_by_label'>

// Reflect a new message on its chat list row: last message, unread, waiting,
// and the 24-hour window.
export function applyMessageToContact(contact: Contact, message: ListMessage) {
  // A late message from the patient doesn't move the chat back in time.
  if (isNewer(message.created_at, contact.last_message_at)) {
    contact.last_message_at = message.created_at
    contact.last_message_direction = message.direction
    const body = message.content?.body
    contact.last_message_preview = body && ['text', 'button_reply', 'template', 'interactive'].includes(message.message_type)
      ? body.slice(0, 100)
      : `[${message.message_type}]`
  }
  if (message.direction === 'incoming') {
    // Mirrors the server's unread count, which excludes already-read
    // messages (e.g. ones the chatbot handled).
    if (message.status !== 'read') {
      contact.unread_count++
      // The wait starts when staff could first see it.
      contact.waiting_since = contact.waiting_since || new Date().toISOString()
    }
    contact.last_inbound_at = laterOf(contact.last_inbound_at, message.created_at)
    if (opensWindow(message)) contact.service_window_open = true
  } else {
    contact.last_send_failed = false
    contact.last_reply_by = message.sent_by_label || message.sent_by_name || contact.last_reply_by
    // Same rule as the server: only a staff reply ends the wait.
    if (message.sent_by_name && message.status !== 'held') contact.waiting_since = undefined
  }
}

// A held message going out moves to where the patient actually gets it: the
// end of the chat, and the top of its chat list row.
export function releaseHeldMessage(messages: Message[], index: number, updated: Message, contacts: Contact[]) {
  messages.splice(index, 1)
  const released = { ...updated, created_at: new Date().toISOString() }
  messages.push(released)
  const contact = contacts.find(c => c.id === released.contact_id)
  if (contact) {
    // Not a staff reply to what the patient just wrote, so any wait stays.
    contact.last_message_at = released.created_at
    contact.last_message_direction = 'outgoing'
    contact.last_message_preview = released.content?.body?.slice(0, 100) || contact.last_message_preview
    contact.last_reply_by = released.sent_by_name || contact.last_reply_by
  }
}
