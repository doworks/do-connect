import { onUnmounted, watch } from 'vue'
import { useContactsStore } from '@/stores/contacts'

// Unread chats show in the browser tab, for staff working in another tab.
export function useUnreadTabTitle() {
  const contactsStore = useContactsStore()
  const baseTitle = document.title
  watch(() => contactsStore.contacts.reduce((sum, contact) => sum + (contact.unread_count > 0 ? 1 : 0), 0), (count) => {
    document.title = count > 0 ? `(${count}) ${baseTitle}` : baseTitle
  }, { immediate: true })
  onUnmounted(() => { document.title = baseTitle })
}

// Browsers only allow this question after a click.
export function askNotificationPermissionOnClick() {
  if (typeof Notification !== 'undefined' && Notification.permission === 'default') {
    document.addEventListener('click', () => { Notification.requestPermission() }, { once: true })
  }
}
