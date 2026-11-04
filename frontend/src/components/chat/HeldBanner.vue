<script setup lang="ts">
import { computed } from 'vue'
import { Hourglass } from 'lucide-vue-next'
import { useContactsStore, type Contact } from '@/stores/contacts'
import { contactTitle } from '@/lib/chatDisplay'

// 24-hour window closed: staff type normally, the message waits for the patient.
defineProps<{ contact: Contact }>()
defineEmits<{ sendTemplate: [] }>()

const contactsStore = useContactsStore()
const heldMessages = computed(() => contactsStore.messages.filter(m => m.status === 'held'))
</script>

<template>
  <div
    class="px-4 py-2 border-t border-amber-500/25 bg-amber-500/10 light:bg-amber-50 light:border-amber-200 flex items-center gap-2"
  >
    <Hourglass class="h-4 w-4 text-amber-400 light:text-amber-600 shrink-0" />
    <div class="flex-1 min-w-0">
      <p class="text-xs font-medium text-amber-300 light:text-amber-800">
        {{ heldMessages.length ? $t('chat.heldWaiting', { name: contactTitle(contact), count: heldMessages.length }) : $t('chat.windowClosedTitle', { name: contactTitle(contact) }) }}
      </p>
      <p class="text-[11px] text-amber-200/70 light:text-amber-700">
        {{ heldMessages.length ? $t('chat.heldWaitingHint') : $t('chat.windowClosedHint') }}
      </p>
    </div>
    <button type="button" class="shrink-0 text-[11px] underline text-amber-300 light:text-amber-800" @click="$emit('sendTemplate')">
      {{ $t('chat.sendTemplateInstead') }}
    </button>
  </div>
</template>
