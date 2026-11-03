<script setup lang="ts">
import { DropdownMenuRoot } from 'reka-ui'

// Default to undefined: a boolean prop otherwise defaults to false, which holds
// the menu shut because reka-ui treats any defined value as controlled.
withDefaults(defineProps<{
  open?: boolean
}>(), { open: undefined })

const emit = defineEmits<{
  'update:open': [value: boolean]
}>()
</script>

<template>
  <DropdownMenuRoot :open="open" @update:open="emit('update:open', $event)">
    <slot />
  </DropdownMenuRoot>
</template>
