<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { Loader2, MessageCirclePlus, User } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { contactsService } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import { normalizePhone, formatPhone, phoneDigits } from '@/lib/phone'
import { useContactsStore } from '@/stores/contacts'
import { useChatSettings } from '@/composables/useChatSettings'

// Shown when the chat list is empty. A search that is a full phone number
// offers to message it, opening the chat with that number.
const { t } = useI18n()
const router = useRouter()
const contactsStore = useContactsStore()
const { defaultCountryCode } = useChatSettings()

const searchedNumber = computed(() => normalizePhone(contactsStore.searchQuery, defaultCountryCode.value))
const searchedNumberLabel = computed(() => formatPhone(searchedNumber.value, defaultCountryCode.value))
const searchLooksLikePhone = computed(() => !!phoneDigits(contactsStore.searchQuery))
const isStartingChat = ref(false)

async function startChatWithNumber() {
  if (!searchedNumber.value || isStartingChat.value) return
  isStartingChat.value = true
  try {
    const response = await contactsService.create({ phone_number: searchedNumber.value, open_existing: true })
    const contact = response.data.data || response.data
    contactsStore.searchQuery = ''
    await contactsStore.fetchContacts()
    if (contact?.id) router.push({ name: 'chat-conversation', params: { contactId: contact.id } })
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.startChatFailed')))
  } finally {
    isStartingChat.value = false
  }
}
</script>

<template>
  <div v-if="searchedNumber" class="p-3">
    <div class="rounded-xl border border-emerald-500/25 bg-emerald-500/[0.07] light:bg-emerald-50 light:border-emerald-200 p-3 text-center">
      <p class="text-xs text-white/60 light:text-gray-600">{{ $t('chat.noContactForNumber') }}</p>
      <Button
        size="sm"
        class="w-full mt-2 bg-emerald-600 hover:bg-emerald-500 text-white"
        :disabled="isStartingChat"
        @click="startChatWithNumber"
      >
        <Loader2 v-if="isStartingChat" class="h-3.5 w-3.5 mr-1.5 animate-spin" />
        <MessageCirclePlus v-else class="h-3.5 w-3.5 mr-1.5" />
        <span dir="ltr">{{ $t('chat.messageNumber', { number: searchedNumberLabel }) }}</span>
      </Button>
    </div>
  </div>
  <div v-else class="p-3 text-center text-white/40 light:text-gray-500">
    <User class="h-6 w-6 mx-auto mb-1.5 opacity-50" />
    <p class="text-sm">{{ searchLooksLikePhone ? $t('chat.typeFullNumber') : $t('chat.noContacts') }}</p>
  </div>
</template>
