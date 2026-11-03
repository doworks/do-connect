<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { messagesService, type SavedToClinic } from '@/services/api'
import { useClinicStore } from '@/stores/clinic'
import { getErrorMessage } from '@/lib/api-utils'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FolderInput, Loader2 } from 'lucide-vue-next'

const props = defineProps<{
  open: boolean
  messageId: string | null
  preview: string
}>()

const emit = defineEmits<{
  'update:open': [open: boolean]
  saved: [marker: SavedToClinic]
}>()

const { t } = useI18n()
const clinic = useClinicStore()
const patient = ref('')
const kind = ref('')
const note = ref('')
const saving = ref(false)

const patients = computed(() => clinic.context?.patients || [])
const kinds = computed(() => clinic.context?.file_kinds || [])

watch(() => props.open, (open) => {
  if (!open) return
  patient.value = clinic.selected?.name || patients.value[0]?.name || ''
  kind.value = ''
  note.value = ''
})

async function save() {
  if (!props.messageId || !patient.value || !kind.value) return
  saving.value = true
  try {
    const response = await messagesService.saveToClinic(props.messageId, { patient: patient.value, kind: kind.value, note: note.value.trim() })
    const marker = response.data.data
    toast.success(t('chat.savedToFileDone', { name: marker.patient_name || patient.value }))
    emit('saved', marker)
    emit('update:open', false)
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.saveToFileFailed')))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle class="text-sm flex items-center gap-2"><FolderInput class="h-4 w-4" />{{ t('chat.saveToFile') }}</DialogTitle>
        <p v-if="preview" class="text-xs text-muted-foreground line-clamp-2 break-words" dir="auto">{{ preview }}</p>
      </DialogHeader>

      <p v-if="!patients.length" class="text-sm text-muted-foreground">{{ t('chat.saveToFileNoPatient') }}</p>
      <div v-else class="space-y-4">
        <div v-if="patients.length > 1" class="space-y-1.5">
          <p class="text-xs font-medium text-muted-foreground">{{ t('chat.saveToFilePatient') }}</p>
          <div class="flex flex-wrap gap-1.5">
            <button
              v-for="p in patients"
              :key="p.name"
              type="button"
              :class="['h-7 px-2.5 rounded-full text-xs border', patient === p.name ? 'bg-emerald-600 border-emerald-600 text-white' : 'border-border hover:bg-muted/60']"
              dir="auto"
              @click="patient = p.name"
            >{{ p.patient_name }}</button>
          </div>
        </div>
        <p v-else class="text-sm"><span class="text-muted-foreground">{{ t('chat.saveToFilePatient') }}:</span> <span class="font-medium" dir="auto">{{ patients[0].patient_name }}</span></p>

        <div class="space-y-1.5">
          <p class="text-xs font-medium text-muted-foreground">{{ t('chat.saveToFileKind') }}</p>
          <div class="flex flex-wrap gap-1.5">
            <button
              v-for="k in kinds"
              :key="k"
              type="button"
              :class="['h-7 px-2.5 rounded-full text-xs border', kind === k ? 'bg-sky-600 border-sky-600 text-white' : 'border-border hover:bg-muted/60']"
              @click="kind = k"
            >{{ k }}</button>
          </div>
        </div>

        <Input v-model="note" :placeholder="t('chat.saveToFileNote')" maxlength="200" dir="auto" />
      </div>

      <DialogFooter>
        <Button size="sm" class="bg-emerald-600 hover:bg-emerald-500 text-white" :disabled="!patient || !kind || saving" @click="save">
          <Loader2 v-if="saving" class="h-4 w-4 animate-spin mr-1" />
          {{ t('chat.saveToFileButton') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
