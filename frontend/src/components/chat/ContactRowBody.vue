<script setup lang="ts">
import { AlertCircle, Clock, MailOpen, MailWarning, MoreVertical } from 'lucide-vue-next'
import { Badge } from '@/components/ui/badge'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import type { Contact } from '@/stores/contacts'
import { formatPhone } from '@/lib/phone'
import { assigneeName, contactLanguage, contactTitle, firstName, formatWaiting, senderColor, senderDot } from '@/lib/chatDisplay'
import { useChatSettings } from '@/composables/useChatSettings'
import { useContactActions } from '@/composables/useContactActions'

// The text side of a chat list row: name and reply language, time and a
// read/unread menu on hover, the last message with who sent it, the unread
// or waiting badge, and the number with the chat's owner. The row itself
// (the "group" the hover effects follow) is the parent's.
defineProps<{
  contact: Contact
  time: string
}>()

const { defaultCountryCode } = useChatSettings()
const { markContactUnread, markContactReadFromList } = useContactActions()
</script>

<template>
  <div class="flex-1 min-w-0">
    <div class="flex items-center justify-between gap-2">
      <div class="flex-1 min-w-0 flex items-center gap-1.5">
        <p
          :class="[
            'min-w-0 text-sm truncate text-white light:text-gray-900',
            contact.unread_count > 0 ? 'font-semibold' : 'font-medium'
          ]"
          :title="contactTitle(contact)"
          dir="auto"
        >
          {{ contactTitle(contact) }}
        </p>
        <span
          v-if="contactLanguage(contact)"
          :class="[
            'shrink-0 h-4 px-1 rounded text-[9px] leading-4 font-bold tracking-wide',
            contactLanguage(contact) === 'AR'
              ? 'bg-violet-500/20 text-violet-300 light:bg-violet-100 light:text-violet-700'
              : 'bg-sky-500/20 text-sky-300 light:bg-sky-100 light:text-sky-700'
          ]"
        >{{ contactLanguage(contact) }}</span>
      </div>
      <span
        :class="[
          'flex-shrink-0 text-[11px] group-hover:hidden',
          contact.unread_count > 0 ? 'text-emerald-400 light:text-emerald-600 font-medium' : 'text-white/40 light:text-gray-500'
        ]"
      >
        {{ time }}
      </span>
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <button
            type="button"
            :aria-label="$t('chat.chatOptions')"
            class="flex flex-shrink-0 h-4 w-0 opacity-0 overflow-hidden group-hover:w-4 group-hover:opacity-100 data-[state=open]:w-4 data-[state=open]:opacity-100 items-center justify-center rounded text-white/60 hover:text-white light:text-gray-500 light:hover:text-gray-900"
            @click.stop
          >
            <MoreVertical class="h-3.5 w-3.5" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" @click.stop>
          <DropdownMenuItem v-if="contact.unread_count === 0" @click="markContactUnread(contact)">
            <MailWarning class="mr-2 h-4 w-4" />
            <span>{{ $t('chat.markAsUnread') }}</span>
          </DropdownMenuItem>
          <DropdownMenuItem v-else @click="markContactReadFromList(contact)">
            <MailOpen class="mr-2 h-4 w-4" />
            <span>{{ $t('chat.markAsRead') }}</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
    <div class="flex items-center justify-between gap-2">
      <p
        :class="[
          'flex-1 min-w-0 text-xs truncate',
          contact.last_send_failed ? 'text-red-400 light:text-red-600' : contact.unread_count > 0 ? 'text-white/75 light:text-gray-800' : 'text-white/50 light:text-gray-500'
        ]"
      >
        <AlertCircle v-if="contact.last_send_failed" class="inline h-3 w-3 -mt-0.5 mr-0.5" />
        <span v-else-if="contact.last_message_direction === 'outgoing' && contact.last_reply_by" :class="['font-medium', senderColor(contact.last_reply_by)]">{{ firstName(contact.last_reply_by) }}: </span>
        <span v-if="contact.last_send_failed">{{ $t('chat.notDelivered') }}: </span>
        <span dir="auto">{{ contact.last_message_preview || contact.phone_number }}</span>
      </p>
      <Badge v-if="contact.unread_count > 0" class="flex-shrink-0 h-5 text-[10px] bg-emerald-500/20 text-emerald-400 light:bg-emerald-100 light:text-emerald-700">
        {{ contact.marked_unread ? '•' : contact.unread_count }}
      </Badge>
      <span
        v-else-if="contact.waiting_since"
        class="flex-shrink-0 inline-flex items-center gap-0.5 h-5 px-1.5 rounded-full text-[10px] font-medium bg-amber-500/15 text-amber-400 light:bg-amber-100 light:text-amber-700"
        :title="$t('chat.waitingForReply')"
      >
        <Clock class="h-2.5 w-2.5" />{{ formatWaiting(contact.waiting_since) }}
      </span>
    </div>
    <div class="flex items-center gap-1.5 mt-0.5 text-[11px] text-white/40 light:text-gray-500">
      <span class="truncate">{{ formatPhone(contact.phone_number, defaultCountryCode) }}</span>
      <span v-if="assigneeName(contact)" class="shrink-0 inline-flex items-center gap-1 ml-auto">
        <span :class="['h-1.5 w-1.5 rounded-full', senderDot(assigneeName(contact))]" />
        {{ firstName(assigneeName(contact)) }}
      </span>
    </div>
  </div>
</template>
