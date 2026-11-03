<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import {
  CalendarOff, Clock, Laptop, Lock, MessageSquareText, Phone, PhoneOff, Plus, Smartphone, Trash2, UserRound, Voicemail, BellRing,
} from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { PageHeader } from '@/components/shared'
import { callRulesService } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import {
  deviceRings, hoursSummary, isClinicOpen, nextOpenLabel, whoRings,
  type CallRules, type DayHours, type RingMode, type StaffRing,
} from '@/lib/callHours'

const { t } = useI18n()
const rules = ref<CallRules | null>(null)
const isSaving = ref(false)
const editing = ref<StaffRing | null>(null)
const now = ref(new Date())

onMounted(async () => {
  setInterval(() => { now.value = new Date() }, 60000)
  try {
    const response = await callRulesService.get()
    rules.value = response.data.data
  } catch (error) {
    toast.error(getErrorMessage(error, t('callHours.loadFailed')))
  }
})

async function save() {
  if (!rules.value) return
  isSaving.value = true
  try {
    await callRulesService.save(rules.value)
    toast.success(t('callHours.saved'))
  } catch (error) {
    toast.error(getErrorMessage(error, t('callHours.saveFailed')))
  } finally {
    isSaving.value = false
  }
}

const open = computed(() => (rules.value ? isClinicOpen(rules.value, now.value) : false))
const ringing = computed(() => (rules.value ? whoRings(rules.value, now.value) : []))

function fill(template: string, language: 'en' | 'ar') {
  return template.replace('{next_open}', rules.value ? nextOpenLabel(rules.value, now.value, language) : '')
}

function addRange(entry: DayHours) {
  entry.ranges.push({ start: '16:00', end: '20:00' })
}

function dayLabel(day: string) {
  return t(`callHours.days.${day}`)
}

const MODES: { value: RingMode; icon: any }[] = [
  { value: 'clinic', icon: Clock },
  { value: 'shift', icon: UserRound },
  { value: 'always', icon: BellRing },
  { value: 'never', icon: PhoneOff },
]

function modeTone(mode: RingMode) {
  return {
    clinic: 'bg-emerald-500/15 text-emerald-300 light:bg-emerald-100 light:text-emerald-800',
    shift: 'bg-sky-500/15 text-sky-300 light:bg-sky-100 light:text-sky-800',
    always: 'bg-violet-500/15 text-violet-300 light:bg-violet-100 light:text-violet-800',
    never: 'bg-white/[0.08] text-white/50 light:bg-gray-100 light:text-gray-500',
  }[mode]
}

function staffSummary(staff: StaffRing) {
  if (!rules.value) return ''
  if (staff.mode === 'shift') return hoursSummary(staff.shift)
  if (staff.mode === 'clinic') return hoursSummary(rules.value.hours)
  return t(`callHours.mode_${staff.mode}_hint`)
}
</script>

<template>
  <div class="flex flex-col h-full bg-[#0a0a0b] light:bg-gray-50">
    <PageHeader :title="t('callHours.title')" :description="t('callHours.subtitle')" :icon="Clock" icon-gradient="bg-gradient-to-br from-emerald-500 to-teal-600 shadow-emerald-500/20">
      <template #actions>
        <Button size="sm" variant="outline" :disabled="isSaving || !rules" @click="save">{{ t('common.save') }}</Button>
      </template>
    </PageHeader>

    <ScrollArea v-if="rules" class="flex-1">
      <div class="p-4 md:p-6 space-y-4 max-w-6xl mx-auto">

        <!-- Right now -->
        <div
          :class="[
            'rounded-xl border p-4 flex flex-wrap items-center gap-x-4 gap-y-2',
            open
              ? 'border-emerald-500/30 bg-emerald-500/10 light:bg-emerald-50 light:border-emerald-200'
              : 'border-amber-500/30 bg-amber-500/10 light:bg-amber-50 light:border-amber-200'
          ]"
        >
          <div class="min-w-[180px]">
            <p :class="['text-sm font-semibold', open ? 'text-emerald-300 light:text-emerald-800' : 'text-amber-300 light:text-amber-800']">
              {{ open ? t('callHours.openNow') : t('callHours.closedNow') }}
            </p>
            <p class="text-xs text-white/50 light:text-gray-600">
              {{ open ? t('callHours.openHint') : t('callHours.closedHint', { next: nextOpenLabel(rules, now, 'en') }) }}
            </p>
          </div>
          <div class="flex-1 min-w-[220px] flex flex-wrap items-center gap-1.5 text-xs">
            <span class="text-white/50 light:text-gray-600">{{ t('callHours.ringsNow') }}</span>
            <span
              v-for="ring in ringing"
              :key="ring.device.id"
              class="inline-flex items-center gap-1 h-6 px-2 rounded-full font-medium bg-white/[0.08] text-white/85 light:bg-white light:text-gray-800 light:border light:border-gray-200"
            >
              <Smartphone v-if="ring.device.kind === 'phone'" class="h-3 w-3" /><Laptop v-else class="h-3 w-3" />
              {{ ring.staff.name }} · {{ ring.device.name }}
            </span>
            <span v-if="!ringing.length" class="font-medium text-amber-300 light:text-amber-800">{{ t('callHours.nobody') }}</span>
          </div>
          <label class="flex items-center gap-2 text-xs font-medium text-white/80 light:text-gray-800">
            <Lock class="h-3.5 w-3.5" /><span :title="t('callHours.closeForEveryoneHint')">{{ t('callHours.closeForEveryone') }}</span>
            <Switch :checked="rules.closed_now" @update:checked="rules!.closed_now = $event" />
          </label>
        </div>

        <div class="grid gap-4 lg:grid-cols-2">
          <!-- Clinic hours -->
          <section class="rounded-xl border border-white/[0.08] bg-white/[0.02] light:bg-white light:border-gray-200 p-4 space-y-3">
            <div>
              <h3 class="font-semibold text-sm text-white light:text-gray-900">{{ t('callHours.clinicHours') }}</h3>
              <p class="text-xs text-white/40 light:text-gray-500">{{ t('callHours.clinicHoursHint', { zone: rules.timezone }) }}</p>
            </div>
            <div class="space-y-1">
              <div v-for="entry in rules.hours" :key="entry.day" class="flex items-center gap-2 min-h-8">
                <Switch :checked="entry.enabled" :aria-label="dayLabel(entry.day)" @update:checked="entry.enabled = $event" />
                <span class="w-[72px] shrink-0 text-xs font-medium text-white/80 light:text-gray-800">{{ dayLabel(entry.day) }}</span>
                <div v-if="entry.enabled" class="flex flex-wrap items-center gap-1">
                  <span v-for="(range, index) in entry.ranges" :key="index" class="inline-flex items-center gap-1">
                    <Input v-model="range.start" type="time" class="h-7 w-[92px] text-xs px-1.5" />
                    <span class="text-white/40 light:text-gray-400">–</span>
                    <Input v-model="range.end" type="time" class="h-7 w-[92px] text-xs px-1.5" />
                    <button type="button" class="p-1 text-white/40 hover:text-red-400 light:text-gray-400" :aria-label="t('common.delete')" @click="entry.ranges.splice(index, 1)"><Trash2 class="h-3 w-3" /></button>
                  </span>
                  <button v-if="entry.ranges.length < 2" type="button" class="inline-flex items-center gap-0.5 text-[11px] text-emerald-400 light:text-emerald-700 hover:underline" @click="addRange(entry)"><Plus class="h-3 w-3" />{{ t('callHours.addRange') }}</button>
                </div>
                <span v-else class="text-xs text-white/35 light:text-gray-400">{{ t('callHours.closed') }}</span>
              </div>
            </div>
            <div class="pt-2 border-t border-white/[0.08] light:border-gray-200 space-y-1.5">
              <p class="flex items-center gap-1.5 text-xs font-medium text-white/80 light:text-gray-800"><CalendarOff class="h-3.5 w-3.5 text-rose-400" />{{ t('callHours.closedDates') }}</p>
              <div v-for="(entry, index) in rules.closed_dates" :key="index" class="flex items-center gap-1.5">
                <Input v-model="entry.date" type="date" class="h-7 w-[132px] text-xs px-1.5" />
                <Input v-model="entry.label" class="h-7 flex-1 text-xs" :placeholder="t('callHours.closedDateLabel')" />
                <button type="button" class="p-1 text-white/40 hover:text-red-400 light:text-gray-400" :aria-label="t('common.delete')" @click="rules!.closed_dates.splice(index, 1)"><Trash2 class="h-3 w-3" /></button>
              </div>
              <button type="button" class="inline-flex items-center gap-0.5 text-[11px] text-emerald-400 light:text-emerald-700 hover:underline" @click="rules!.closed_dates.push({ date: '', label: '' })"><Plus class="h-3 w-3" />{{ t('callHours.addClosedDate') }}</button>
            </div>
          </section>

          <!-- Outside hours -->
          <section class="rounded-xl border border-white/[0.08] bg-white/[0.02] light:bg-white light:border-gray-200 p-4 space-y-3">
            <div>
              <h3 class="font-semibold text-sm text-white light:text-gray-900">{{ t('callHours.outsideHours') }}</h3>
              <p class="text-xs text-white/40 light:text-gray-500">{{ t('callHours.outsideHoursHint') }}</p>
            </div>
            <div class="grid gap-1.5">
              <button
                v-for="option in [{ value: 'message', icon: MessageSquareText }, { value: 'voice', icon: Voicemail }, { value: 'ring', icon: Phone }]"
                :key="option.value"
                type="button"
                :class="[
                  'flex items-start gap-2 rounded-lg border p-2 text-left',
                  rules.outside_action === option.value
                    ? 'border-emerald-500 bg-emerald-500/10 light:bg-emerald-50'
                    : 'border-white/[0.08] light:border-gray-200 hover:bg-white/[0.04] light:hover:bg-gray-50'
                ]"
                @click="rules!.outside_action = option.value as CallRules['outside_action']"
              >
                <component :is="option.icon" class="h-4 w-4 mt-0.5 shrink-0 text-emerald-400 light:text-emerald-700" />
                <span>
                  <span class="block text-xs font-medium text-white light:text-gray-900">{{ t(`callHours.action_${option.value}`) }}</span>
                  <span class="block text-[11px] text-white/45 light:text-gray-500">{{ t(`callHours.action_${option.value}_hint`) }}</span>
                </span>
              </button>
            </div>

            <div v-if="rules.outside_action !== 'ring'" class="space-y-2 pt-2 border-t border-white/[0.08] light:border-gray-200">
              <p class="text-xs font-medium text-white/80 light:text-gray-800">{{ t('callHours.closedMessage') }}</p>
              <Textarea v-model="rules.messages.closed.en" :rows="3" class="text-xs" />
              <Textarea v-model="rules.messages.closed.ar" :rows="3" class="text-xs" dir="rtl" />
              <p class="text-[11px] text-white/40 light:text-gray-500">{{ t('callHours.nextOpenHint') }}</p>
              <p class="text-xs font-medium text-white/80 light:text-gray-800 pt-1">{{ t('callHours.missedMessage') }}</p>
              <Textarea v-model="rules.messages.missed.en" :rows="2" class="text-xs" />
              <Textarea v-model="rules.messages.missed.ar" :rows="2" class="text-xs" dir="rtl" />
              <label class="flex items-center gap-2 text-[11px] text-white/60 light:text-gray-600">
                {{ t('callHours.gapBefore') }}
                <Input v-model.number="rules.message_gap_hours" type="number" :min="0" :max="72" class="h-7 w-14 text-xs px-1.5" />
                {{ t('callHours.gapAfter') }}
              </label>
              <div class="rounded-lg bg-white/[0.04] light:bg-gray-100 p-2 space-y-1.5">
                <p class="text-[11px] text-white/45 light:text-gray-500">{{ t('callHours.patientSees') }}</p>
                <p class="chat-bubble chat-bubble-outgoing text-xs !max-w-full">{{ fill(rules.messages.closed.en, 'en') }}</p>
                <p class="chat-bubble chat-bubble-outgoing text-xs !max-w-full" dir="rtl">{{ fill(rules.messages.closed.ar, 'ar') }}</p>
              </div>
            </div>
          </section>
        </div>

        <!-- Who rings -->
        <section class="rounded-xl border border-white/[0.08] bg-white/[0.02] light:bg-white light:border-gray-200">
          <div class="p-4 pb-2">
            <h3 class="font-semibold text-sm text-white light:text-gray-900">{{ t('callHours.staffTitle') }}</h3>
            <p class="text-xs text-white/40 light:text-gray-500">{{ t('callHours.staffHint') }}</p>
          </div>
          <div class="overflow-x-auto">
            <table class="w-full min-w-[640px] text-xs">
              <thead>
                <tr class="text-left text-white/40 light:text-gray-500">
                  <th class="px-4 py-2 font-medium">{{ t('callHours.colStaff') }}</th>
                  <th class="px-2 py-2 font-medium">{{ t('callHours.colRings') }}</th>
                  <th class="px-2 py-2 font-medium">{{ t('callHours.colDevices') }}</th>
                  <th class="px-4 py-2 font-medium text-right">{{ t('callHours.colNow') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="staff in rules.staff"
                  :key="staff.id"
                  class="border-t border-white/[0.06] light:border-gray-100 cursor-pointer hover:bg-white/[0.04] light:hover:bg-gray-50"
                  @click="editing = staff"
                >
                  <td class="px-4 py-2">
                    <p class="font-medium text-white light:text-gray-900">{{ staff.name }}</p>
                    <p class="text-white/40 light:text-gray-500">{{ staff.role }}</p>
                  </td>
                  <td class="px-2 py-2">
                    <span :class="['inline-block h-5 px-1.5 rounded-full text-[11px] leading-5 font-medium', modeTone(staff.mode)]">{{ t(`callHours.mode_${staff.mode}`) }}</span>
                    <p class="mt-0.5 text-white/40 light:text-gray-500">{{ staffSummary(staff) }}</p>
                  </td>
                  <td class="px-2 py-2">
                    <span
                      v-for="device in staff.devices"
                      :key="device.id"
                      :class="[
                        'inline-flex items-center gap-1 mr-1 mb-0.5 h-5 px-1.5 rounded-full text-[11px]',
                        device.mode === 'never' ? 'bg-white/[0.04] text-white/30 line-through light:bg-gray-50 light:text-gray-400' : 'bg-white/[0.08] text-white/70 light:bg-gray-100 light:text-gray-700'
                      ]"
                    >
                      <Smartphone v-if="device.kind === 'phone'" class="h-3 w-3" /><Laptop v-else class="h-3 w-3" />{{ device.name }}
                    </span>
                  </td>
                  <td class="px-4 py-2 text-right">
                    <span v-if="!staff.available" class="text-white/40 light:text-gray-500">{{ t('callHours.away') }}</span>
                    <span v-else-if="ringing.some(ring => ring.staff.id === staff.id)" class="inline-flex items-center gap-1 font-medium text-emerald-400 light:text-emerald-700"><BellRing class="h-3 w-3" />{{ t('callHours.rings') }}</span>
                    <span v-else class="text-white/40 light:text-gray-500">{{ t('callHours.quiet') }}</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </ScrollArea>

    <!-- One person's schedule and devices -->
    <Dialog :open="!!editing" @update:open="(value) => !value && (editing = null)">
      <DialogContent v-if="editing && rules" class="max-w-lg max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle class="text-base">{{ editing.name }}</DialogTitle>
          <DialogDescription class="text-xs">{{ editing.role }}</DialogDescription>
        </DialogHeader>
        <div class="space-y-3 text-xs">
          <div class="grid grid-cols-2 gap-1.5">
            <button
              v-for="mode in MODES"
              :key="mode.value"
              type="button"
              :class="[
                'flex items-start gap-2 rounded-lg border p-2 text-left',
                editing.mode === mode.value ? 'border-emerald-500 bg-emerald-500/10 light:bg-emerald-50' : 'border-border hover:bg-accent'
              ]"
              @click="editing!.mode = mode.value"
            >
              <component :is="mode.icon" class="h-4 w-4 mt-0.5 shrink-0" />
              <span>
                <span class="block font-medium">{{ t(`callHours.mode_${mode.value}`) }}</span>
                <span class="block text-[11px] text-muted-foreground">{{ t(`callHours.mode_${mode.value}_hint`) }}</span>
              </span>
            </button>
          </div>

          <div v-if="editing.mode === 'shift'" class="space-y-1">
            <p class="font-medium">{{ t('callHours.shift') }}</p>
            <div v-for="entry in editing.shift" :key="entry.day" class="flex items-center gap-2 min-h-8">
              <Switch :checked="entry.enabled" :aria-label="dayLabel(entry.day)" @update:checked="entry.enabled = $event" />
              <span class="w-[72px] shrink-0 font-medium">{{ dayLabel(entry.day) }}</span>
              <div v-if="entry.enabled" class="flex flex-wrap items-center gap-1">
                <span v-for="(range, index) in entry.ranges" :key="index" class="inline-flex items-center gap-1">
                  <Input v-model="range.start" type="time" class="h-7 w-[92px] text-xs px-1.5" />
                  <span class="text-muted-foreground">–</span>
                  <Input v-model="range.end" type="time" class="h-7 w-[92px] text-xs px-1.5" />
                </span>
                <button v-if="entry.ranges.length < 2" type="button" class="inline-flex items-center text-[11px] text-emerald-500 hover:underline" @click="addRange(entry)"><Plus class="h-3 w-3" />{{ t('callHours.addRange') }}</button>
              </div>
              <span v-else class="text-muted-foreground">{{ t('callHours.off') }}</span>
            </div>
          </div>

          <div class="space-y-1.5">
            <p class="font-medium">{{ t('callHours.devicesTitle') }}</p>
            <div v-for="device in editing.devices" :key="device.id" class="flex items-center gap-2 rounded-lg border border-border p-2">
              <Smartphone v-if="device.kind === 'phone'" class="h-4 w-4 shrink-0" /><Laptop v-else class="h-4 w-4 shrink-0" />
              <span class="flex-1 min-w-0">
                <span class="block font-medium truncate">{{ device.name }}</span>
                <span class="block text-[11px] text-muted-foreground">{{ deviceRings(rules, editing, device, now) ? t('callHours.rings') : t('callHours.quiet') }}</span>
              </span>
              <select v-model="device.mode" class="h-7 rounded-md border border-input bg-transparent px-1.5 text-xs">
                <option value="follow" class="text-gray-900">{{ t('callHours.deviceFollow') }}</option>
                <option value="never" class="text-gray-900">{{ t('callHours.deviceNever') }}</option>
              </select>
            </div>
          </div>
          <p class="text-[11px] text-muted-foreground">{{ t('callHours.awayHint') }}</p>
        </div>
      </DialogContent>
    </Dialog>
  </div>
</template>
