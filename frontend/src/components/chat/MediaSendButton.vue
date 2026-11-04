<script setup lang="ts">
import { Hourglass, Send } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'

// Send in the attach dialog. While the 24-hour window is closed the file
// waits for the patient's reply.
defineProps<{ holds: boolean; uploading: boolean }>()
</script>

<template>
  <Button
    :disabled="uploading"
    :class="holds && 'bg-amber-600 hover:bg-amber-500 text-white'"
  >
    <template v-if="uploading">{{ $t('chat.sending') }}...</template>
    <template v-else-if="holds">
      <Hourglass class="mr-2 h-4 w-4" />{{ $t('chat.sendWhenTheyReply') }}
    </template>
    <template v-else>
      <Send class="mr-2 h-4 w-4" />{{ $t('chat.send') }}
    </template>
  </Button>
</template>
