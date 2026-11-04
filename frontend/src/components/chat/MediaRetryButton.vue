<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { messagesService } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import { useContactsStore, type Message } from '@/stores/contacts'

// A file that failed to download: fetch it from WhatsApp again.
const props = defineProps<{ message: Message }>()

const { t } = useI18n()
const contactsStore = useContactsStore()
const retrying = ref(false)

async function retryMedia() {
  const message = props.message
  retrying.value = true
  try {
    const response = await messagesService.retryMedia(message.contact_id, message.id)
    const index = contactsStore.messages.findIndex(m => m.id === message.id)
    if (index !== -1) {
      contactsStore.messages[index] = { ...contactsStore.messages[index], media_url: response.data.data.media_url, media_missing: false }
    }
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.downloadFailed')))
  } finally {
    retrying.value = false
  }
}
</script>

<template>
  <button
    type="button"
    class="block mb-1 text-xs font-medium underline text-amber-300 light:text-amber-700 disabled:opacity-50"
    :disabled="retrying"
    @click="retryMedia"
  >
    {{ $t('chat.downloadAgain') }}
  </button>
</template>
