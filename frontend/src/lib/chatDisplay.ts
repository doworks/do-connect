// How contacts and messages are labelled in the chat screen: the reply
// language, the name without its language prefix, and who sent what.
import type { Contact, Message } from '@/stores/contacts'

// A contact's name may start with the language to reply in ("AR · Sara").
const LANGUAGE_PREFIX = /^(AR|EN) · /

export function contactLanguage(contact: Contact): string {
  const prefix = (contact.name || '').match(LANGUAGE_PREFIX)
  if (prefix) return prefix[1]
  if (contact.tags?.includes('Arabic')) return 'AR'
  if (contact.tags?.includes('English')) return 'EN'
  return ''
}

export function contactTitle(contact: Contact): string {
  return (contact.name || contact.phone_number).replace(LANGUAGE_PREFIX, '')
}

const SENDER_COLORS = [
  'text-sky-200 light:text-sky-700',
  'text-amber-200 light:text-amber-700',
  'text-pink-200 light:text-pink-700',
  'text-violet-200 light:text-violet-700',
  'text-cyan-200 light:text-cyan-700',
  'text-orange-200 light:text-orange-700',
  'text-rose-200 light:text-rose-700',
  'text-yellow-200 light:text-yellow-700',
]

const SENDER_DOTS = ['bg-sky-500', 'bg-amber-500', 'bg-pink-500', 'bg-violet-500', 'bg-cyan-500', 'bg-orange-500', 'bg-rose-500', 'bg-yellow-500']

function senderIndex(name: string): number {
  let hash = 2166136261
  for (let i = 0; i < name.length; i++) hash = Math.imul(hash ^ name.charCodeAt(i), 16777619)
  return Math.abs(hash) % SENDER_COLORS.length
}

export function senderColor(name: string): string {
  return SENDER_COLORS[senderIndex(name)]
}

export function senderDot(name: string): string {
  return SENDER_DOTS[senderIndex(name)]
}

export function senderName(message: Message): string {
  if (message.direction !== 'outgoing') return ''
  return message.sent_by_label || message.sent_by_name || ''
}

export function isAutomatedSender(message: Message): boolean {
  return !!message.sent_by_label
}

export function firstName(name: string): string {
  return (name || '').split(' ')[0]
}

export function formatWaiting(since?: string): string {
  if (!since) return ''
  const minutes = Math.max(1, Math.floor((Date.now() - new Date(since).getTime()) / 60000))
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

export function assigneeName(contact: Contact): string {
  return contact.assigned_user_name || ''
}

export function formatInfoTime(dateStr?: string): string {
  if (!dateStr) return '—'
  const date = new Date(dateStr)
  return date.toLocaleString('en-GB', { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' })
}
