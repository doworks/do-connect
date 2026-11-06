<script setup lang="ts">
import { UserCheck } from 'lucide-vue-next'
import type { Contact } from '@/stores/contacts'
import { formatPhone } from '@/lib/phone'
import { assigneeName, firstName, senderColor } from '@/lib/chatDisplay'
import { useChatSettings } from '@/composables/useChatSettings'

// Under the name in the chat header: the number, who owns the chat and who replied last.
defineProps<{ contact: Contact }>()

const { defaultCountryCode } = useChatSettings()
</script>

<template>
  <p class="text-[11px] text-white/50 light:text-gray-500 flex items-center gap-1.5 whitespace-nowrap overflow-hidden">
    <span dir="ltr">{{ formatPhone(contact.phone_number, defaultCountryCode) }}</span>
    <template v-if="assigneeName(contact)">
      <span class="opacity-40">·</span>
      <span class="inline-flex items-center gap-1">
        <UserCheck class="h-3 w-3" />{{ assigneeName(contact) }}
      </span>
    </template>
    <template v-if="contact.last_reply_by">
      <span class="opacity-40">·</span>
      <span>{{ $t('chat.lastReplyBy') }}
        <span :class="['font-medium', senderColor(contact.last_reply_by)]">{{ firstName(contact.last_reply_by) }}</span>
      </span>
    </template>
  </p>
</template>
