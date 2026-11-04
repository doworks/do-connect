<script setup lang="ts">
import { Forward, FolderInput } from 'lucide-vue-next'
import type { Message } from '@/stores/contacts'

// Above a message: whether it was forwarded, and whether its file was saved to the patient's record.
defineProps<{ message: Message }>()
</script>

<template>
  <div v-if="message.forwarded" class="flex items-center gap-1 text-[11px] italic text-muted-foreground mb-0.5"><Forward class="h-3 w-3" />{{ $t('chat.forwarded') }}</div>
  <div v-if="message.saved_to_clinic" class="flex items-center gap-1 text-[11px] text-sky-400 light:text-sky-700 mb-0.5"><FolderInput class="h-3 w-3" />{{ $t('chat.savedToFileLabel', { kind: message.saved_to_clinic.kind, name: message.saved_to_clinic.by_name }) }}</div>
</template>
