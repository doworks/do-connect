<script setup lang="ts">
import { MailWarning, UserCheck } from 'lucide-vue-next'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { useAuthStore } from '@/stores/auth'
import type { Contact } from '@/stores/contacts'
import { useContactActions } from '@/composables/useContactActions'

// Extra entries in the chat header's options menu.
defineProps<{ contact: Contact }>()
defineEmits<{ assignToMe: [] }>()

const authStore = useAuthStore()
const { markContactUnread } = useContactActions()
</script>

<template>
  <DropdownMenuItem
    v-if="contact.assigned_user_id !== authStore.user?.id"
    @click="$emit('assignToMe')"
  >
    <UserCheck class="mr-2 h-4 w-4" />
    <span>{{ $t('chat.assignToMe') }}</span>
  </DropdownMenuItem>
  <DropdownMenuItem @click="markContactUnread(contact)">
    <MailWarning class="mr-2 h-4 w-4" />
    <span>{{ $t('chat.markAsUnread') }}</span>
  </DropdownMenuItem>
</template>
