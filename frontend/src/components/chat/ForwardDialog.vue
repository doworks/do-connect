<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { contactsService, messagesService, type ForwardResult } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import { normalizePhone, formatPhone, phoneDigits } from '@/lib/phone'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Check, Forward, Loader2, Search, UserPlus, X } from 'lucide-vue-next'

interface ChatOption {
  id: string
  name: string
  phone: string
}

const props = defineProps<{
  open: boolean
  messageId: string | null
  preview: string
  defaultCountryCode: string
}>()

const emit = defineEmits<{ 'update:open': [open: boolean] }>()

const MAX_TARGETS = 10
const { t } = useI18n()
const search = ref('')
const options = ref<ChatOption[]>([])
const chosen = ref<ChatOption[]>([])
const loading = ref(false)
const sending = ref(false)
const failures = ref<{ name: string; reason: string }[]>([])
let searchSeq = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined

const typedNumber = computed(() => phoneDigits(search.value) ? normalizePhone(search.value, props.defaultCountryCode) : '')
const showTypedNumber = computed(() => !!typedNumber.value && !options.value.some(o => o.phone === typedNumber.value))
const isChosen = (id: string) => chosen.value.some(c => c.id === id)

watch(() => props.open, (open) => {
  if (!open) return
  search.value = ''
  chosen.value = []
  failures.value = []
  runSearch()
})

watch(search, () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(runSearch, 250)
})

async function runSearch() {
  const seq = ++searchSeq
  loading.value = true
  try {
    const response = await contactsService.list({ search: search.value.trim(), limit: 20 })
    if (seq !== searchSeq) return
    const data = response.data.data || response.data
    options.value = (data.contacts || []).map((c: any) => ({
      id: c.id,
      name: c.name || c.whatsapp_name || c.profile_name || formatPhone(c.phone_number, props.defaultCountryCode),
      phone: c.phone_number,
    }))
  } catch {
    if (seq === searchSeq) options.value = []
  } finally {
    if (seq === searchSeq) loading.value = false
  }
}

function toggle(option: ChatOption) {
  if (isChosen(option.id)) {
    chosen.value = chosen.value.filter(c => c.id !== option.id)
  } else if (chosen.value.length < MAX_TARGETS) {
    chosen.value = [...chosen.value, option]
  } else {
    toast.error(t('chat.forwardLimit', { count: MAX_TARGETS }))
  }
}

async function addTypedNumber() {
  try {
    const response = await contactsService.create({ phone_number: typedNumber.value, open_existing: true })
    const contact = response.data.data || response.data
    toggle({ id: contact.id, name: formatPhone(contact.phone_number, props.defaultCountryCode), phone: contact.phone_number })
    search.value = ''
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.startChatFailed')))
  }
}

async function send() {
  if (!props.messageId || !chosen.value.length) return
  sending.value = true
  try {
    const response = await messagesService.forward(props.messageId, chosen.value.map(c => c.id))
    const results: ForwardResult[] = response.data.data?.results || []
    const names = Object.fromEntries(chosen.value.map(c => [c.id, c.name]))
    const sent = results.filter(r => r.status === 'sent').length
    const held = results.filter(r => r.status === 'held').length
    failures.value = results.filter(r => r.status === 'failed').map(r => ({ name: names[r.contact_id] || '', reason: r.reason || '' }))
    if (sent) toast.success(t('chat.forwardSent', { count: sent }))
    if (held) toast.success(t('chat.forwardHeld', { count: held }))
    // Keep the dialog open to show what didn't go, and why.
    if (!failures.value.length) emit('update:open', false)
  } catch (error) {
    toast.error(getErrorMessage(error, t('chat.forwardFailed')))
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="max-w-md p-0 gap-0">
      <DialogHeader class="px-4 pt-4 pb-2">
        <DialogTitle class="text-sm flex items-center gap-2"><Forward class="h-4 w-4" />{{ t('chat.forwardTitle') }}</DialogTitle>
        <p class="text-xs text-muted-foreground line-clamp-2 break-words" dir="auto">{{ preview }}</p>
      </DialogHeader>

      <div class="px-4 pb-2">
        <div class="relative">
          <Search class="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input v-model="search" class="pl-8 h-9" :placeholder="t('chat.forwardSearch')" />
        </div>
        <div v-if="chosen.length" class="flex flex-wrap gap-1 mt-2">
          <span v-for="c in chosen" :key="c.id" class="inline-flex items-center gap-1 h-6 pl-2 pr-1 rounded-full text-xs bg-emerald-600/15 text-emerald-400 light:text-emerald-700">
            {{ c.name }}
            <button type="button" class="rounded-full p-0.5 hover:bg-emerald-600/20" :aria-label="t('common.remove')" @click="toggle(c)"><X class="h-3 w-3" /></button>
          </span>
        </div>
      </div>

      <div class="max-h-72 overflow-y-auto border-t">
        <button
          v-if="showTypedNumber"
          type="button"
          class="w-full flex items-center gap-3 px-4 py-2 text-sm hover:bg-muted/60 text-left"
          @click="addTypedNumber"
        >
          <UserPlus class="h-4 w-4 text-emerald-500" />
          <span>{{ t('chat.forwardToNumber', { number: formatPhone(typedNumber, defaultCountryCode) }) }}</span>
        </button>
        <div v-if="loading && !options.length" class="flex justify-center py-6"><Loader2 class="h-4 w-4 animate-spin text-muted-foreground" /></div>
        <button
          v-for="option in options"
          :key="option.id"
          type="button"
          class="w-full flex items-center gap-3 px-4 py-2 text-sm hover:bg-muted/60 text-left"
          @click="toggle(option)"
        >
          <span :class="['h-4 w-4 shrink-0 rounded border flex items-center justify-center', isChosen(option.id) ? 'bg-emerald-600 border-emerald-600 text-white' : 'border-muted-foreground/50']">
            <Check v-if="isChosen(option.id)" class="h-3 w-3" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="block truncate" dir="auto">{{ option.name }}</span>
            <span class="block text-xs text-muted-foreground" dir="ltr">{{ formatPhone(option.phone, defaultCountryCode) }}</span>
          </span>
        </button>
        <p v-if="!loading && !options.length && !showTypedNumber" class="px-4 py-6 text-center text-xs text-muted-foreground">{{ t('chat.forwardNoChats') }}</p>
      </div>

      <div v-if="failures.length" class="border-t px-4 py-2 space-y-1">
        <p class="text-xs font-medium text-red-400 light:text-red-600">{{ t('chat.forwardNotSent') }}</p>
        <p v-for="(f, i) in failures" :key="i" class="text-xs text-muted-foreground"><span class="font-medium text-foreground" dir="auto">{{ f.name }}</span> · {{ f.reason }}</p>
      </div>

      <DialogFooter class="px-4 py-3 border-t">
        <Button size="sm" class="bg-emerald-600 hover:bg-emerald-500 text-white" :disabled="!chosen.length || sending" @click="send">
          <Loader2 v-if="sending" class="h-4 w-4 animate-spin mr-1" />
          <Forward v-else class="h-4 w-4 mr-1" />
          {{ chosen.length ? t('chat.forwardTo', { count: chosen.length }) : t('chat.forward') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
