import { computed, inject, provide, ref, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import type { Message } from '@/stores/contacts'
import type { SavedToClinic } from '@/services/api'
import { useClinicStore } from '@/stores/clinic'

// What can be done with one message besides replying: see its details
// (who sent it, when it arrived), forward it, or save its file to the
// patient's record. The chat screen provides this; the message parts and
// the dialogs use it.
export interface MessageTools {
  infoMessage: Ref<Message | null>
  forwardingMessage: Ref<Message | null>
  isForwardOpen: Ref<boolean>
  forwardPreview: ComputedRef<string>
  savingMessage: Ref<Message | null>
  isSaveToFileOpen: Ref<boolean>
  getMessageContent: (message: Message) => string
  showInfo: (message: Message) => void
  canForward: (message: Message) => boolean
  forwardMessage: (message: Message) => void
  canSaveToFile: (message: Message) => boolean
  saveToFile: (message: Message) => void
  onSavedToFile: (marker: SavedToClinic) => void
}

interface MessageHelpers {
  getMessageContent: (message: Message) => string
  isMediaMessage: (message: Message) => boolean
}

const KEY: InjectionKey<MessageTools> = Symbol('messageTools')

export function provideMessageTools({ getMessageContent, isMediaMessage }: MessageHelpers): MessageTools {
  const clinic = useClinicStore()
  const infoMessage = ref<Message | null>(null)

  function showInfo(message: Message) {
    infoMessage.value = message
  }

  const forwardingMessage = ref<Message | null>(null)
  const isForwardOpen = ref(false)

  function canForward(message: Message): boolean {
    if (message.status === 'held' || ['reaction', 'call'].includes(message.message_type)) return false
    if (isMediaMessage(message)) return !!message.media_url
    return !!getMessageContent(message)
  }

  function forwardMessage(message: Message) {
    forwardingMessage.value = message
    isForwardOpen.value = true
  }

  const forwardPreview = computed(() => {
    const message = forwardingMessage.value
    if (!message) return ''
    return getMessageContent(message) || `[${message.message_type}]`
  })

  const savingMessage = ref<Message | null>(null)
  const isSaveToFileOpen = ref(false)

  function canSaveToFile(message: Message): boolean {
    return !!clinic.context && isMediaMessage(message) && !!message.media_url && message.message_type !== 'sticker'
  }

  function saveToFile(message: Message) {
    savingMessage.value = message
    isSaveToFileOpen.value = true
  }

  function onSavedToFile(marker: SavedToClinic) {
    if (savingMessage.value) savingMessage.value.saved_to_clinic = marker
  }

  const tools: MessageTools = {
    infoMessage, forwardingMessage, isForwardOpen, forwardPreview, savingMessage, isSaveToFileOpen,
    getMessageContent, showInfo, canForward, forwardMessage, canSaveToFile, saveToFile, onSavedToFile,
  }
  provide(KEY, tools)
  return tools
}

export function useMessageTools(): MessageTools {
  const tools = inject(KEY)
  if (!tools) throw new Error('useMessageTools needs provideMessageTools in a parent component')
  return tools
}
