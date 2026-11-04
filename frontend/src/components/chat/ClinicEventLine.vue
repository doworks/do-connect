<script setup lang="ts">
import { CalendarClock } from 'lucide-vue-next'
import type { Message } from '@/stores/contacts'

// Something that happened in the clinic system; the patient doesn't see these.
defineProps<{ message: Message; time: string }>()
</script>

<template>
  <div class="flex justify-center my-1">
    <span class="inline-flex items-center gap-1.5 max-w-[85%] px-2.5 py-1 rounded-md text-[11px] bg-indigo-500/15 text-indigo-200 light:bg-indigo-50 light:text-indigo-800">
      <CalendarClock class="h-3 w-3 shrink-0" />
      <span>{{ message.content?.body }} · {{ time }}</span>
    </span>
  </div>
</template>
