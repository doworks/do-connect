<script setup lang="ts">
import { DropdownMenuRoot } from 'reka-ui'

// An absent boolean prop is cast to false, and reka-ui treats any defined
// `open` as controlled, so default to undefined to keep the menu uncontrolled
// unless the parent binds v-model:open.
withDefaults(defineProps<{
  open?: boolean
}>(), {
  open: undefined,
})

const emit = defineEmits<{
  'update:open': [value: boolean]
}>()
</script>

<template>
  <DropdownMenuRoot :open="open" @update:open="emit('update:open', $event)">
    <slot />
  </DropdownMenuRoot>
</template>
