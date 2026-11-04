<script setup lang="ts">
// Messages arrived below while reading older ones; click to go down to them.
defineProps<{ count: number }>()
</script>

<template>
  <button
    v-if="count > 0"
    type="button"
    class="absolute bottom-3 left-1/2 -translate-x-1/2 z-10 px-3 h-7 rounded-full text-xs font-medium shadow-md bg-emerald-600 text-white hover:bg-emerald-500"
  >
    {{ $t('chat.newBelow', { count }) }}
  </button>
</template>
