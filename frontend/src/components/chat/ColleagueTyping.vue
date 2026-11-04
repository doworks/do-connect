<script setup lang="ts">
import { useContactsStore } from '@/stores/contacts'

// Someone else is answering this patient.
const contactsStore = useContactsStore()
</script>

<template>
  <div
    v-if="contactsStore.agentTyping"
    class="px-4 py-1 border-t border-sky-500/20 bg-sky-500/10 light:bg-sky-50 text-xs text-sky-300 light:text-sky-800"
  >
    {{ $t('chat.colleagueTyping', { name: contactsStore.agentTyping }) }}
  </div>
</template>
