<script setup lang="ts">
import { Hourglass } from 'lucide-vue-next'

// 24-hour window closed: the file waits for the patient, like text.
</script>

<template>
  <p class="flex items-start gap-2 text-xs text-amber-400 light:text-amber-700">
    <Hourglass class="h-3.5 w-3.5 mt-0.5 shrink-0" />
    <span>{{ $t('chat.mediaHeldHint') }}</span>
  </p>
</template>
