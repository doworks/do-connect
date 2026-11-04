<script setup lang="ts">
import { Bot } from 'lucide-vue-next'
import type { Message } from '@/stores/contacts'
import { isAutomatedSender, senderColor, senderName } from '@/lib/chatDisplay'
import { useMessageTools } from '@/composables/useMessageTools'

// Who sent an outgoing message, beside its time. Opens the message info.
defineProps<{ message: Message }>()

const { showInfo } = useMessageTools()
</script>

<template>
  <button v-if="senderName(message)" type="button" :class="['chat-bubble-sender', isAutomatedSender(message) ? '' : senderColor(senderName(message))]" @click="showInfo(message)"><Bot v-if="isAutomatedSender(message)" class="h-3 w-3" />{{ senderName(message) }}</button>
</template>
