<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import {
  X, ExternalLink, CalendarClock, CalendarPlus, CheckCircle2, XCircle, Wallet, Layers, ShieldAlert,
  ClipboardList, FileText, StickyNote, UserPlus, ArrowLeft, Send, Loader2, Search, CreditCard, Languages, AlertTriangle
} from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { clinicService } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import { formatPhone } from '@/lib/phone'
import { useClinicStore } from '@/stores/clinic'
import type { Contact } from '@/stores/contacts'
import { formatClock, formatDay, formatMoney, formatVisitTime } from '@/lib/clinic'

const props = defineProps<{ contact: Contact; view: string }>()
const emit = defineEmits<{
  close: []
  'update:view': [view: string]
  draft: [text: string]
}>()

const { t } = useI18n()
const clinic = useClinicStore()
const patient = computed(() => clinic.selected)
const busy = ref('')

const isScheduling = computed(() => props.view === 'book' || props.view === 'reschedule')
const viewTitle = computed(() => t(`clinic.view_${props.view}`))

// Booking
interface Slot { time: string; state: 'free' | 'busy' | 'full' }
const practitioners = ref<{ name: string; label: string; color: string }[]>([])
const appointmentTypes = ref<{ name: string; duration: number }[]>([])
const practitioner = ref('')
const appointmentType = ref('')
const day = ref('')
const slots = ref<Slot[]>([])
const slot = ref('')
const notifies = ref<string[]>([])

const days = computed(() => {
  const today = new Date()
  return Array.from({ length: 7 }, (_, i) => {
    const date = new Date(today.getFullYear(), today.getMonth(), today.getDate() + i)
    const value = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
    return { value, weekday: date.toLocaleDateString('en-GB', { weekday: 'short' }), date: date.getDate() }
  })
})

async function openScheduling() {
  const response = await clinicService.bookingOptions(patient.value?.name || '')
  const data = response.data.data
  practitioners.value = data.practitioners
  appointmentTypes.value = data.appointment_types
  const current = props.view === 'reschedule' ? patient.value?.next_appointment : null
  practitioner.value = current?.practitioner || data.default_practitioner || practitioners.value[0]?.name || ''
  appointmentType.value = current?.type || appointmentTypes.value[0]?.name || ''
  day.value = day.value || days.value[1].value
  slot.value = ''
  await loadSlots()
}

async function loadSlots() {
  if (!practitioner.value || !day.value) return
  slot.value = ''
  const response = await clinicService.slots({
    practitioner: practitioner.value,
    date: day.value,
    appointment_type: appointmentType.value,
    appointment: props.view === 'reschedule' ? patient.value?.next_appointment?.name : undefined,
  })
  slots.value = response.data.data.slots
  notifies.value = response.data.data.notifies || []
}

watch(() => props.view, (view) => {
  if (view === 'book' || view === 'reschedule') openScheduling()
}, { immediate: true })
watch(() => clinic.selected?.name, () => {
  if (isScheduling.value) openScheduling()
})

const selectedDoctor = computed(() => practitioners.value.find(p => p.name === practitioner.value))

async function saveBooking() {
  if (!patient.value || !slot.value) return
  busy.value = 'book'
  try {
    const payload = {
      patient: patient.value.name,
      practitioner: practitioner.value,
      appointment_type: appointmentType.value,
      date: day.value,
      time: slot.value,
    }
    const response = props.view === 'reschedule'
      ? await clinicService.reschedule({ ...payload, appointment: patient.value.next_appointment!.name })
      : await clinicService.book(payload)
    const result = response.data.data
    toast.success(props.view === 'reschedule' ? t('clinic.rescheduled') : t('clinic.booked'))
    if (result.draft) emit('draft', result.draft)
    await clinic.reload()
    emit('update:view', 'overview')
  } catch (error) {
    toast.error(getErrorMessage(error, t('clinic.actionFailed')))
  } finally {
    busy.value = ''
  }
}

async function runAction(key: string, call: () => Promise<any>, done: string) {
  busy.value = key
  try {
    const response = await call()
    toast.success(done)
    if (response.data?.data?.draft) emit('draft', response.data.data.draft)
    await clinic.reload()
  } catch (error) {
    toast.error(getErrorMessage(error, t('clinic.actionFailed')))
  } finally {
    busy.value = ''
  }
}

const cancelling = ref(false)
const cancelReason = ref('')

function confirmVisit() {
  const visit = patient.value?.next_appointment
  if (visit) runAction('confirm', () => clinicService.confirm(visit.name), t('clinic.confirmedDone'))
}

function cancelVisit() {
  const visit = patient.value?.next_appointment
  if (!visit || !cancelReason.value.trim()) return
  runAction('cancel', () => clinicService.cancel(visit.name, cancelReason.value), t('clinic.cancelledDone'))
  cancelling.value = false
  cancelReason.value = ''
}

function sendInvoice(invoice: string) {
  runAction(`invoice-${invoice}`, () => clinicService.sendDocument('Sales Invoice', invoice), t('clinic.documentSent'))
}

function sendHealthForm() {
  if (patient.value) runAction('form', () => clinicService.sendHealthForm(patient.value!.name), t('clinic.documentSent'))
}

// New patient
const form = ref({ first_name: '', last_name: '', sex: '', dob: '', cpr: '', language: '' })
const duplicates = ref<{ name: string; patient_name: string; hint: string }[]>([])

watch(() => props.view, (view) => {
  if (view === 'new') {
    form.value = { first_name: props.contact.whatsapp_name || props.contact.profile_name || '', last_name: '', sex: '', dob: '', cpr: '', language: 'EN' }
    duplicates.value = []
  }
}, { immediate: true })

watch(() => form.value.cpr, async (cpr) => {
  if (props.view !== 'new' || cpr.length < 9) {
    duplicates.value = []
    return
  }
  const response = await clinicService.findPatients(cpr)
  duplicates.value = response.data.data.patients
})

async function createPatient() {
  busy.value = 'new'
  try {
    await clinicService.createPatient({ ...form.value, phone: props.contact.phone_number })
    toast.success(t('clinic.patientCreated'))
    await clinic.reload()
    emit('update:view', 'overview')
  } catch (error) {
    toast.error(getErrorMessage(error, t('clinic.actionFailed')))
  } finally {
    busy.value = ''
  }
}

// Add this number to a patient
const searchText = ref('')
const searchResults = ref<{ name: string; patient_name: string; hint: string }[]>([])

async function runSearch() {
  if (searchText.value.trim().length < 2) {
    searchResults.value = []
    return
  }
  const response = await clinicService.findPatients(searchText.value.trim())
  searchResults.value = response.data.data.patients
}

async function linkPatient(name: string) {
  await runAction('link', () => clinicService.linkNumber(name, props.contact.phone_number), t('clinic.numberAdded'))
  emit('update:view', 'overview')
}

const frappeUrl = computed(() => patient.value
  ? clinicService.frappeLink('Patient', patient.value.name)
  : '')
</script>

<template>
  <div class="flex flex-col bg-card h-full w-[320px] shrink-0 border-l border-white/[0.08] light:border-gray-200">
    <div class="h-14 px-3 border-b border-white/[0.08] light:border-gray-200 flex items-center gap-1">
      <Button v-if="view !== 'overview'" variant="ghost" size="icon" class="h-7 w-7" :aria-label="t('common.back')" @click="emit('update:view', 'overview')">
        <ArrowLeft class="h-4 w-4" />
      </Button>
      <h3 class="flex-1 font-medium text-sm truncate">{{ viewTitle }}</h3>
      <a v-if="frappeUrl" :href="frappeUrl" target="_blank" rel="noopener" class="inline-flex items-center gap-1 text-[11px] font-medium text-sky-300 light:text-sky-700 hover:underline">
        {{ t('clinic.openFrappe') }}<ExternalLink class="h-3 w-3" />
      </a>
      <Button variant="ghost" size="icon" class="h-7 w-7" :aria-label="t('common.close')" @click="emit('close')">
        <X class="h-4 w-4" />
      </Button>
    </div>

    <ScrollArea class="flex-1">
      <div class="p-3 space-y-3 text-xs">

        <!-- Overview -->
        <template v-if="view === 'overview' && patient">
          <div v-if="(clinic.context?.patients.length || 0) > 1" class="flex flex-wrap gap-1">
            <button
              v-for="member in clinic.context!.patients"
              :key="member.name"
              type="button"
              :class="[
                'h-6 px-2 rounded-full font-medium',
                member.name === patient.name
                  ? 'bg-indigo-600 text-white'
                  : 'bg-white/[0.08] text-white/70 light:bg-gray-100 light:text-gray-700'
              ]"
              @click="clinic.select(member.name)"
            >{{ member.patient_name.split(' ')[0] }} <span class="opacity-70">{{ member.age }}</span></button>
          </div>

          <div>
            <p class="text-sm font-semibold" dir="auto">{{ patient.patient_name }}</p>
            <p class="text-muted-foreground flex flex-wrap gap-x-2 mt-0.5">
              <span>{{ [patient.age, patient.sex].filter(Boolean).join(' · ') }}</span>
              <span dir="ltr">{{ t('clinic.file') }} {{ patient.file_number }}</span>
              <span v-if="patient.cpr_masked" class="inline-flex items-center gap-1" dir="ltr"><CreditCard class="h-3 w-3" />{{ patient.cpr_masked }}</span>
            </p>
            <p class="flex items-center gap-1 mt-1 text-muted-foreground">
              <Languages class="h-3 w-3" />{{ t('clinic.replyIn') }}
              <span class="font-medium text-foreground">{{ patient.language === 'AR' ? t('clinic.arabic') : t('clinic.english') }}</span>
            </p>
          </div>

          <div v-for="flag in patient.flags" :key="flag.label" class="flex items-center gap-1.5 rounded-md px-2 py-1.5 font-medium bg-red-500/15 text-red-300 light:bg-red-50 light:text-red-700">
            <ShieldAlert class="h-3.5 w-3.5 shrink-0" />{{ flag.label }}
          </div>

          <!-- Next visit -->
          <section class="rounded-lg border border-sky-500/30 bg-sky-500/[0.07] light:bg-sky-50 light:border-sky-200 p-2.5 space-y-1.5">
            <h4 class="flex items-center gap-1.5 text-xs font-semibold text-sky-300 light:text-sky-800"><CalendarClock class="h-3.5 w-3.5" />{{ t('clinic.nextVisit') }}</h4>
            <template v-if="patient.next_appointment">
              <p class="text-sm font-medium">{{ formatVisitTime(patient.next_appointment) }}</p>
              <p class="text-muted-foreground flex items-center gap-1.5">
                <i v-if="patient.next_appointment.color" class="h-2 w-2 rounded-full" :style="{ background: patient.next_appointment.color }" />
                {{ patient.next_appointment.practitioner_name }}<template v-if="patient.next_appointment.type"> · {{ patient.next_appointment.type }}</template>
              </p>
              <div class="flex flex-wrap gap-1 pt-0.5">
                <Button v-if="!patient.next_appointment.confirmed" size="sm" class="h-6 px-2 text-[11px] bg-emerald-600 hover:bg-emerald-500 text-white" :disabled="busy === 'confirm'" @click="confirmVisit">
                  <CheckCircle2 class="h-3 w-3 mr-1" />{{ t('clinic.confirm') }}
                </Button>
                <span v-else class="inline-flex items-center gap-1 h-6 px-1.5 font-medium text-emerald-300 light:text-emerald-700"><CheckCircle2 class="h-3 w-3" />{{ t('clinic.confirmed') }}</span>
                <Button size="sm" variant="outline" class="h-6 px-2 text-[11px]" @click="emit('update:view', 'reschedule')">{{ t('clinic.reschedule') }}</Button>
                <Button size="sm" variant="outline" class="h-6 px-2 text-[11px] text-red-400 light:text-red-700" @click="cancelling = !cancelling">
                  <XCircle class="h-3 w-3 mr-1" />{{ t('common.cancel') }}
                </Button>
              </div>
              <form v-if="cancelling" class="flex gap-1 pt-1" @submit.prevent="cancelVisit">
                <Input v-model="cancelReason" class="h-7 text-xs" :placeholder="t('clinic.cancelReason')" />
                <Button type="submit" size="sm" class="h-7 px-2 text-[11px]" variant="destructive" :disabled="!cancelReason.trim()">{{ t('clinic.cancelVisit') }}</Button>
              </form>
            </template>
            <template v-else>
              <p class="text-muted-foreground">{{ t('clinic.noneBooked') }}</p>
              <Button size="sm" class="h-6 px-2 text-[11px] bg-emerald-600 hover:bg-emerald-500 text-white" @click="emit('update:view', 'book')">
                <CalendarPlus class="h-3 w-3 mr-1" />{{ t('clinic.book') }}
              </Button>
            </template>
            <p v-if="patient.last_visit" class="text-muted-foreground border-t border-sky-500/20 light:border-sky-200 pt-1.5">
              {{ t('clinic.lastVisit') }}: {{ formatDay(patient.last_visit.date) }} · {{ patient.last_visit.practitioner_name }}
            </p>
          </section>

          <!-- Money -->
          <section v-if="patient.balance" class="rounded-lg border border-amber-500/30 bg-amber-500/[0.07] light:bg-amber-50 light:border-amber-200 p-2.5 space-y-1.5">
            <h4 class="flex items-center gap-1.5 text-xs font-semibold text-amber-300 light:text-amber-800"><Wallet class="h-3.5 w-3.5" />{{ t('clinic.balance') }}</h4>
            <dl class="grid grid-cols-[1fr_auto] gap-y-0.5 tabular-nums">
              <dt class="text-muted-foreground">{{ t('clinic.outstanding') }}</dt>
              <dd :class="['font-semibold', patient.balance.due > 0 ? 'text-red-300 light:text-red-700' : '']">{{ formatMoney(patient.balance.due, patient.balance.currency) }}</dd>
              <template v-if="patient.balance.credit > 0">
                <dt class="text-muted-foreground">{{ t('clinic.advance') }}</dt>
                <dd class="font-semibold text-emerald-300 light:text-emerald-700">{{ formatMoney(patient.balance.credit, patient.balance.currency) }}</dd>
              </template>
              <template v-if="patient.balance.awaiting_insurance">
                <dt class="text-muted-foreground">{{ t('clinic.awaitingInsurance') }}</dt>
                <dd>{{ formatMoney(patient.balance.awaiting_insurance, patient.balance.currency) }}</dd>
              </template>
            </dl>
            <ul v-if="patient.balance.invoices.length" class="space-y-1 border-t border-amber-500/20 light:border-amber-200 pt-1.5">
              <li v-for="invoice in patient.balance.invoices" :key="invoice.name" class="flex items-center gap-1.5">
                <FileText class="h-3 w-3 shrink-0 text-muted-foreground" />
                <span class="flex-1 min-w-0 truncate">{{ formatDay(invoice.date) }} · <span class="tabular-nums">{{ formatMoney(invoice.outstanding, patient.balance.currency) }}</span></span>
                <button type="button" class="inline-flex items-center gap-1 font-medium text-sky-300 light:text-sky-700 hover:underline" @click="sendInvoice(invoice.name)">
                  <Send class="h-3 w-3" />{{ t('clinic.send') }}
                </button>
              </li>
            </ul>
          </section>

          <!-- Sessions -->
          <section v-if="patient.sessions?.length" class="rounded-lg border border-violet-500/30 bg-violet-500/[0.07] light:bg-violet-50 light:border-violet-200 p-2.5 space-y-1.5">
            <h4 class="flex items-center gap-1.5 text-xs font-semibold text-violet-300 light:text-violet-800"><Layers class="h-3.5 w-3.5" />{{ t('clinic.sessions') }}</h4>
            <div v-for="session in patient.sessions" :key="session.label">
              <p class="flex justify-between"><span>{{ session.label }}</span><span class="tabular-nums font-medium">{{ session.done }}/{{ session.total }}</span></p>
              <div class="h-1 mt-1 rounded-full bg-violet-500/20 overflow-hidden"><i class="block h-full bg-violet-500" :style="{ width: `${session.done / session.total * 100}%` }" /></div>
            </div>
          </section>

          <!-- Everything else -->
          <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
            <template v-if="patient.insurance">
              <dt class="text-muted-foreground">{{ t('clinic.insurance') }}</dt>
              <dd>{{ patient.insurance.company }}<span v-if="patient.insurance.expires" class="text-muted-foreground"> · {{ t('clinic.expires') }} {{ formatDay(patient.insurance.expires) }}</span></dd>
            </template>
            <template v-if="patient.health_form">
              <dt class="text-muted-foreground">{{ t('clinic.healthForm') }}</dt>
              <dd class="flex items-center gap-2">
                <span>{{ patient.health_form.status }}</span>
                <button type="button" class="inline-flex items-center gap-1 font-medium text-sky-300 light:text-sky-700 hover:underline" @click="sendHealthForm"><Send class="h-3 w-3" />{{ t('clinic.send') }}</button>
              </dd>
            </template>
            <template v-if="patient.open_tasks">
              <dt class="text-muted-foreground">{{ t('clinic.tasks') }}</dt>
              <dd class="inline-flex items-center gap-1"><ClipboardList class="h-3 w-3" />{{ t('clinic.openTasks', { count: patient.open_tasks }) }}</dd>
            </template>
          </dl>

          <section v-if="patient.notes?.length" class="space-y-1">
            <h4 class="flex items-center gap-1.5 text-xs font-semibold text-muted-foreground"><StickyNote class="h-3.5 w-3.5" />{{ t('clinic.frontDeskNotes') }}</h4>
            <p v-for="note in patient.notes" :key="note.text" class="rounded-md bg-white/[0.05] light:bg-gray-100 px-2 py-1.5" dir="auto">{{ note.text }} <span class="text-muted-foreground">· {{ note.by }}</span></p>
          </section>
        </template>

        <!-- Not registered -->
        <template v-else-if="view === 'overview'">
          <p class="text-sm font-semibold">{{ t('clinic.notRegistered') }}</p>
          <p class="text-muted-foreground">{{ t('clinic.notRegisteredHint', { phone: formatPhone(contact.phone_number, '973') }) }}</p>
          <div class="grid gap-1.5">
            <Button v-if="clinic.context?.can_create" size="sm" class="bg-emerald-600 hover:bg-emerald-500 text-white" @click="emit('update:view', 'new')"><UserPlus class="h-3.5 w-3.5 mr-1.5" />{{ t('clinic.newPatient') }}</Button>
            <Button v-if="clinic.context?.can_link" size="sm" variant="outline" @click="emit('update:view', 'link')"><Search class="h-3.5 w-3.5 mr-1.5" />{{ t('clinic.addToExisting') }}</Button>
          </div>
        </template>

        <!-- Book or reschedule -->
        <template v-else-if="isScheduling">
          <p v-if="patient" class="font-medium">
            {{ patient.patient_name }}
            <span v-if="view === 'reschedule' && patient.next_appointment" class="block font-normal text-muted-foreground">{{ t('clinic.movingFrom') }} {{ formatVisitTime(patient.next_appointment) }}</span>
          </p>
          <div>
            <p class="mb-1 text-muted-foreground">{{ t('clinic.doctor') }}</p>
            <div class="flex flex-wrap gap-1">
              <button
                v-for="doctor in practitioners"
                :key="doctor.name"
                type="button"
                :class="[
                  'inline-flex items-center gap-1 h-6 px-2 rounded-full font-medium',
                  practitioner === doctor.name ? 'bg-sky-600 text-white' : 'bg-white/[0.08] text-white/70 light:bg-gray-100 light:text-gray-700'
                ]"
                @click="practitioner = doctor.name; loadSlots()"
              >
                <i class="h-2 w-2 rounded-full" :style="{ background: doctor.color }" />{{ doctor.label }}
              </button>
            </div>
          </div>
          <div>
            <p class="mb-1 text-muted-foreground">{{ t('clinic.visitType') }}</p>
            <select v-model="appointmentType" class="w-full h-7 rounded-md border border-input bg-transparent px-1.5" @change="loadSlots">
              <option v-for="type in appointmentTypes" :key="type.name" :value="type.name" class="text-gray-900">{{ type.name }} · {{ type.duration }} min</option>
            </select>
          </div>
          <div class="grid grid-cols-7 gap-0.5">
            <button
              v-for="option in days"
              :key="option.value"
              type="button"
              :class="[
                'rounded-md py-1 text-center',
                day === option.value ? 'bg-sky-600 text-white' : 'bg-white/[0.06] text-white/70 light:bg-gray-100 light:text-gray-700'
              ]"
              @click="day = option.value; loadSlots()"
            >
              <span class="block text-[9px] opacity-80">{{ option.weekday }}</span>
              <span class="block font-semibold">{{ option.date }}</span>
            </button>
          </div>
          <div class="grid grid-cols-4 gap-1">
            <button
              v-for="option in slots"
              :key="option.time"
              type="button"
              :disabled="option.state !== 'free'"
              :title="option.state === 'full' ? t('clinic.slotFull') : option.state === 'busy' ? t('clinic.slotBusy') : ''"
              :class="[
                'h-7 rounded-md text-[11px] font-medium tabular-nums',
                slot === option.time ? 'bg-emerald-600 text-white'
                  : option.state === 'free' ? 'bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25 light:bg-emerald-50 light:text-emerald-800 light:hover:bg-emerald-100'
                  : 'bg-white/[0.03] text-white/25 line-through light:bg-gray-50 light:text-gray-300'
              ]"
              @click="slot = option.time"
            >{{ formatClock(option.time).replace(' AM', 'a').replace(' PM', 'p') }}</button>
          </div>
          <p v-for="note in notifies" :key="note" class="flex gap-1.5 rounded-md px-2 py-1.5 bg-amber-500/10 text-amber-300 light:bg-amber-50 light:text-amber-800">
            <AlertTriangle class="h-3.5 w-3.5 shrink-0" />{{ note }}
          </p>
          <Button class="w-full bg-emerald-600 hover:bg-emerald-500 text-white" size="sm" :disabled="!slot || busy === 'book'" @click="saveBooking">
            <Loader2 v-if="busy === 'book'" class="h-3.5 w-3.5 mr-1.5 animate-spin" />
            <template v-if="slot">{{ view === 'reschedule' ? t('clinic.moveTo') : t('clinic.bookFor') }} {{ formatDay(day) }}, {{ formatClock(slot) }}</template>
            <template v-else>{{ t('clinic.pickTime') }}</template>
          </Button>
          <p class="text-muted-foreground">{{ t('clinic.bookingRules', { doctor: selectedDoctor?.label || '' }) }}</p>
        </template>

        <!-- New patient -->
        <form v-else-if="view === 'new'" class="space-y-2" @submit.prevent="createPatient">
          <p class="text-muted-foreground" dir="ltr">{{ formatPhone(contact.phone_number, '973') }}</p>
          <label class="block"><span class="block mb-0.5 text-muted-foreground">{{ t('clinic.firstName') }}</span><Input id="clinic-first-name" v-model="form.first_name" class="h-7 text-xs" dir="auto" required /></label>
          <label class="block"><span class="block mb-0.5 text-muted-foreground">{{ t('clinic.lastName') }}</span><Input id="clinic-last-name" v-model="form.last_name" class="h-7 text-xs" dir="auto" /></label>
          <p class="text-[11px] text-amber-300 light:text-amber-700">{{ t('clinic.checkWhatsappName') }}</p>
          <div class="grid grid-cols-2 gap-2">
            <label class="block"><span class="block mb-0.5 text-muted-foreground">{{ t('clinic.sex') }}</span>
              <select id="clinic-sex" v-model="form.sex" class="w-full h-7 rounded-md border border-input bg-transparent px-1.5" required>
                <option value="" class="text-gray-900" />
                <option value="Female" class="text-gray-900">{{ t('clinic.female') }}</option>
                <option value="Male" class="text-gray-900">{{ t('clinic.male') }}</option>
              </select>
            </label>
            <label class="block"><span class="block mb-0.5 text-muted-foreground">{{ t('clinic.dob') }}</span><Input id="clinic-dob" v-model="form.dob" type="date" class="h-7 text-xs" /></label>
          </div>
          <label class="block"><span class="block mb-0.5 text-muted-foreground">{{ t('clinic.cpr') }}</span><Input id="clinic-cpr" v-model="form.cpr" class="h-7 text-xs" inputmode="numeric" dir="ltr" /></label>
          <div v-if="duplicates.length" class="rounded-md p-2 space-y-1 bg-amber-500/10 text-amber-300 light:bg-amber-50 light:text-amber-800">
            <p class="font-medium flex items-center gap-1"><AlertTriangle class="h-3.5 w-3.5" />{{ t('clinic.possibleDuplicate') }}</p>
            <button v-for="match in duplicates" :key="match.name" type="button" class="block w-full text-left hover:underline" @click="linkPatient(match.name)">
              {{ match.patient_name }} · {{ match.hint }} · <span class="font-medium">{{ t('clinic.addNumberToThem') }}</span>
            </button>
          </div>
          <Button type="submit" size="sm" class="w-full bg-emerald-600 hover:bg-emerald-500 text-white" :disabled="busy === 'new' || !form.first_name.trim() || !form.sex">{{ t('clinic.createPatient') }}</Button>
        </form>

        <!-- Add number to an existing patient -->
        <template v-else-if="view === 'link'">
          <form class="flex gap-1" @submit.prevent="runSearch">
            <Input id="clinic-search" v-model="searchText" class="h-7 text-xs" :placeholder="t('clinic.searchPatient')" />
            <Button type="submit" size="sm" class="h-7 px-2" variant="outline"><Search class="h-3.5 w-3.5" /></Button>
          </form>
          <button
            v-for="match in searchResults"
            :key="match.name"
            type="button"
            class="w-full text-left rounded-md p-2 bg-white/[0.05] hover:bg-white/[0.1] light:bg-gray-100 light:hover:bg-gray-200"
            @click="linkPatient(match.name)"
          >
            <span class="block font-medium">{{ match.patient_name }}</span>
            <span class="block text-muted-foreground">{{ match.hint }}</span>
          </button>
        </template>
      </div>
    </ScrollArea>
  </div>
</template>
