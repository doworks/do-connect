import { nextTick, type Ref } from 'vue'
import { useContactsStore } from '@/stores/contacts'

interface ComposerRefs {
  messageInput: Ref<string>
  messageInputRef: Ref<HTMLTextAreaElement | null>
  autoResize: () => void
}

// Each chat keeps its own draft, so text typed for one patient can't go to
// another. Also puts ready-made text (canned replies, suggested drafts) into
// the composer to edit before sending.
export function useChatDrafts({ messageInput, messageInputRef, autoResize }: ComposerRefs) {
  const contactsStore = useContactsStore()
  const drafts = new Map<string, string>()

  // Call when the open chat changes: keeps the old chat's text, brings back the new one's.
  function switchDraft(newId: unknown, oldId: unknown) {
    if (oldId) drafts.set(String(oldId), messageInput.value)
    messageInput.value = newId ? drafts.get(String(newId)) || '' : ''
    contactsStore.clearReplyingTo()
    nextTick(autoResize)
  }

  // The draft went out.
  function forgetDraft(contactId: string) {
    drafts.delete(contactId)
  }

  function focusComposer() {
    nextTick(() => {
      autoResize()
      messageInputRef.value?.focus()
    })
  }

  // Replace what is typed with this text.
  function useDraft(text: string) {
    messageInput.value = text
    focusComposer()
  }

  // Add this text after what is typed.
  function insertIntoComposer(text: string) {
    const before = messageInput.value.trimEnd()
    messageInput.value = before ? `${before} ${text}` : text
    focusComposer()
  }

  return { switchDraft, forgetDraft, useDraft, insertIntoComposer }
}
