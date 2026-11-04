import { wsService } from '@/services/websocket'
import { useContactsStore } from '@/stores/contacts'
import { useAuthStore } from '@/stores/auth'

// Typing tells colleagues in the same chat who is replying, a few seconds apart.
export function useTypingNotice(autoResize: () => void) {
  const contactsStore = useContactsStore()
  const authStore = useAuthStore()
  let lastTypingSentAt = 0

  function onComposerInput() {
    autoResize()
    const contact = contactsStore.currentContact
    if (contact && Date.now() - lastTypingSentAt > 2500) {
      lastTypingSentAt = Date.now()
      wsService.sendAgentTyping(contact.id, authStore.user?.full_name || '')
    }
  }

  return { onComposerInput }
}

// A file pasted into the composer or dropped on the chat goes the same way as one picked.
export function useFilePasteDrop(prepareFile: (file: File) => unknown) {
  function handleComposerPaste(event: ClipboardEvent) {
    const file = event.clipboardData?.files?.[0]
    if (file) {
      event.preventDefault()
      prepareFile(file)
    }
  }

  function handleChatDrop(event: DragEvent) {
    const file = event.dataTransfer?.files?.[0]
    if (file) prepareFile(file)
  }

  return { handleComposerPaste, handleChatDrop }
}
