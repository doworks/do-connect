<script setup lang="ts">
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { lateArrivalAt } from '@/lib/messageTime'
import { formatInfoTime, senderName } from '@/lib/chatDisplay'
import { useChatSettings } from '@/composables/useChatSettings'
import { useMessageTools } from '@/composables/useMessageTools'
import ForwardDialog from './ForwardDialog.vue'
import SaveToFileDialog from './SaveToFileDialog.vue'

// The dialogs behind a message's extra actions: save to the patient's file,
// forward, and message info (who sent it, and when it was sent, delivered,
// read, or reached us late).
const { defaultCountryCode } = useChatSettings()
const {
  infoMessage, forwardingMessage, isForwardOpen, forwardPreview, savingMessage, isSaveToFileOpen,
  getMessageContent, onSavedToFile,
} = useMessageTools()
</script>

<template>
  <SaveToFileDialog
    v-model:open="isSaveToFileOpen"
    :message-id="savingMessage?.id || null"
    :preview="savingMessage ? getMessageContent(savingMessage) : ''"
    @saved="onSavedToFile"
  />
  <ForwardDialog
    v-model:open="isForwardOpen"
    :message-id="forwardingMessage?.id || null"
    :preview="forwardPreview"
    :default-country-code="defaultCountryCode"
  />

  <!-- Message info -->
  <Dialog :open="!!infoMessage" @update:open="(open) => !open && (infoMessage = null)">
    <DialogContent class="max-w-xs">
      <DialogHeader>
        <DialogTitle class="text-sm">{{ $t('chat.messageInfo') }}</DialogTitle>
      </DialogHeader>
      <dl v-if="infoMessage" class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs">
        <template v-if="infoMessage.direction === 'outgoing'">
          <dt class="text-muted-foreground">{{ $t('chat.infoSentBy') }}</dt>
          <dd class="font-medium">{{ senderName(infoMessage) || '—' }}</dd>
        </template>
        <template v-if="infoMessage.template_name">
          <dt class="text-muted-foreground">{{ $t('chat.infoTemplate') }}</dt>
          <dd class="break-all">{{ infoMessage.template_name }}</dd>
        </template>
        <dt class="text-muted-foreground">{{ $t('chat.infoSent') }}</dt>
        <dd>{{ formatInfoTime(infoMessage.created_at) }}</dd>
        <template v-if="infoMessage.direction === 'incoming'">
          <template v-if="lateArrivalAt(infoMessage)">
            <dt class="text-muted-foreground">{{ $t('chat.infoReceived') }}</dt>
            <dd class="font-medium text-amber-400 light:text-amber-700">{{ formatInfoTime(lateArrivalAt(infoMessage)!) }}</dd>
          </template>
        </template>
        <template v-else>
          <dt class="text-muted-foreground">{{ $t('chat.infoDelivered') }}</dt>
          <dd>{{ formatInfoTime(infoMessage.delivered_at) }}</dd>
          <dt class="text-muted-foreground">{{ $t('chat.infoRead') }}</dt>
          <dd>{{ formatInfoTime(infoMessage.read_at) }}</dd>
        </template>
      </dl>
      <p v-if="infoMessage && lateArrivalAt(infoMessage)" class="text-[11px] text-muted-foreground">
        {{ $t('chat.infoLateHint') }}
      </p>
    </DialogContent>
  </Dialog>
</template>
