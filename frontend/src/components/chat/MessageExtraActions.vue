<script setup lang="ts">
import { Forward, FolderInput, Info } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import type { Message } from '@/stores/contacts'
import { lateArrivalAt } from '@/lib/messageTime'
import { useMessageTools } from '@/composables/useMessageTools'

// Hover buttons beside a message, after Reply: when a patient's message
// arrived late, forward, and save its file to the patient's record.
defineProps<{ message: Message }>()

const { showInfo, canForward, forwardMessage, canSaveToFile, saveToFile } = useMessageTools()
</script>

<template>
  <Button
    v-if="lateArrivalAt(message)"
    variant="ghost"
    size="icon"
    class="h-6 w-6 text-amber-400 light:text-amber-600"
    :title="$t('chat.arrivedLate')"
    @click="showInfo(message)"
  >
    <Info class="h-3 w-3" />
  </Button>
  <Button
    v-if="canForward(message)"
    variant="ghost"
    size="icon"
    class="h-6 w-6"
    :title="$t('chat.forward')"
    @click="forwardMessage(message)"
  >
    <Forward class="h-3 w-3" />
  </Button>
  <Button
    v-if="canSaveToFile(message)"
    variant="ghost"
    size="icon"
    class="h-6 w-6"
    :title="$t('chat.saveToFile')"
    @click="saveToFile(message)"
  >
    <FolderInput class="h-3 w-3" />
  </Button>
</template>
