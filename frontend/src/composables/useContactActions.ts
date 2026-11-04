import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { contactsService } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import { useContactsStore, type Contact } from '@/stores/contacts'
import { useAuthStore } from '@/stores/auth'
import { useUsersStore } from '@/stores/users'

// Read / unread from the chat list and the chat header, and who a chat belongs to.
export function useContactActions() {
  const router = useRouter()
  const { t } = useI18n()
  const contactsStore = useContactsStore()
  const authStore = useAuthStore()
  const usersStore = useUsersStore()

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

  // The owner fields of a contact once it is assigned to userId (or to nobody).
  function assigneeFields(userId: string | null) {
    return {
      assigned_user_id: userId || undefined,
      assigned_user_name: userId
        ? (userId === authStore.user?.id ? authStore.user?.full_name : usersStore.users.find(u => u.id === userId)?.full_name)
        : undefined
    }
  }

  return { markContactUnread, markContactReadFromList, assigneeFields }
}
