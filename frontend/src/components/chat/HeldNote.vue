<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { Hourglass } from 'lucide-vue-next'
import { messagesService } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import { useContactsStore, type Message } from '@/stores/contacts'

// Under a message held until the patient replies, with a way to cancel it.
const props = defineProps<{ message: Message }>()

const { t } = useI18n()
const contactsStore = useContactsStore()

async function cancelHeldMessage() {
  const message = props.message
  try {
    await messagesService.cancelHeld(message.contact_id, message.id)
    contactsStore.removeMessage(message.id)
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.cancelHeldFailed')))
  }
}
</script>

<template>
  <span class="chat-bubble-held-note">
    <Hourglass class="h-3 w-3 shrink-0" />
    <span class="flex-1">{{ $t('chat.heldNote') }}</span>
    <button type="button" class="underline shrink-0" @click="cancelHeldMessage">{{ $t('common.cancel') }}</button>
  </span>
</template>
