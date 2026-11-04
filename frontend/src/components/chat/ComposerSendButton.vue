<script setup lang="ts">
import { Hourglass, Send } from 'lucide-vue-next'

// The composer's send button. While the 24-hour window is closed it says the
// message will go when the patient replies.
defineProps<{ holds: boolean }>()
</script>

<template>
  <button
    type="submit"
    :class="[
      'h-9 rounded-lg flex items-center justify-center gap-1.5 transition-colors disabled:opacity-50',
      holds
        ? 'px-3 shrink-0 text-xs font-medium text-white bg-amber-600 hover:bg-amber-500'
        : 'w-9 bg-emerald-600 hover:bg-emerald-500 light:bg-emerald-500 light:hover:bg-emerald-600'
    ]"
  >
    <template v-if="holds">
      <Hourglass class="w-3.5 h-3.5" />{{ $t('chat.sendWhenTheyReply') }}
    </template>
    <Send v-else class="w-4 h-4 text-white" />
  </button>
</template>
