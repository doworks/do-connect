import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { contactsService } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import { useContactsStore, type Contact } from '@/stores/contacts'

// Read / unread from the chat list and the chat header.
export function useContactActions() {
  const router = useRouter()
  const { t } = useI18n()
  const contactsStore = useContactsStore()

  async function markContactUnread(contact: Contact) {
    try {
      await contactsService.markUnread(contact.id)
      contactsStore.markContactUnread(contact.id)
      if (contactsStore.currentContact?.id === contact.id) router.push({ name: 'chat-conversation' })
    } catch (error) {
      toast.error(getErrorMessage(error, t('chat.markUnreadFailed')))
    }
  }

  async function markContactReadFromList(contact: Contact) {
    try {
      await contactsService.markRead(contact.id)
      contactsStore.markContactRead(contact.id)
    } catch (error) {
      toast.error(getErrorMessage(error, t('chat.markUnreadFailed')))
    }
  }

  return { markContactUnread, markContactReadFromList }
}
