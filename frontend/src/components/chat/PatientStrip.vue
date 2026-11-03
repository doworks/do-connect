<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { CalendarClock, CalendarPlus, ChevronDown, Layers, PanelRight, UserPlus, Wallet, ShieldAlert } from 'lucide-vue-next'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useClinicStore } from '@/stores/clinic'
import { formatMoney, formatVisitTime } from '@/lib/clinic'

const emit = defineEmits<{ open: [view: string] }>()
const { t } = useI18n()
const clinic = useClinicStore()

const patient = computed(() => clinic.selected)
const others = computed(() => clinic.context?.patients || [])
const firstSession = computed(() => patient.value?.sessions?.[0])
const due = computed(() => patient.value?.balance?.due || 0)
const credit = computed(() => patient.value?.balance?.credit || 0)
</script>

<template>
  <div
    v-if="clinic.context"
    class="flex-shrink-0 flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-1.5 border-b border-white/[0.08] light:border-gray-200 bg-[#0a0a0b] light:bg-gray-50 text-xs"
  >
    <!-- Registered -->
    <template v-if="patient">
      <DropdownMenu v-if="others.length > 1">
        <DropdownMenuTrigger as-child>
          <button type="button" class="inline-flex items-center gap-1 h-6 px-2 rounded-full font-medium bg-indigo-500/20 text-indigo-200 light:bg-indigo-100 light:text-indigo-800">
            {{ patient.patient_name }}
            <span class="opacity-70">· {{ t('clinic.onThisNumber', { count: others.length }) }}</span>
            <ChevronDown class="h-3 w-3" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          <DropdownMenuItem v-for="member in others" :key="member.name" @click="clinic.select(member.name)">
            <span class="font-medium">{{ member.patient_name }}</span>
            <span class="ml-2 text-muted-foreground">{{ member.age }}</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <span class="text-white/50 light:text-gray-500" dir="ltr">{{ t('clinic.file') }} {{ patient.file_number }}</span>

      <span v-if="patient.flags?.length" class="inline-flex items-center gap-1 text-red-300 light:text-red-700 font-medium">
        <ShieldAlert class="h-3.5 w-3.5" />{{ patient.flags[0].label }}
      </span>

      <button
        type="button"
        class="inline-flex items-center gap-1 hover:underline"
        :class="patient.next_appointment ? 'text-white/85 light:text-gray-800' : 'text-white/45 light:text-gray-500'"
        @click="emit('open', patient.next_appointment ? 'reschedule' : 'book')"
      >
        <CalendarClock class="h-3.5 w-3.5 text-sky-300 light:text-sky-700" />
        <template v-if="patient.next_appointment">
          {{ formatVisitTime(patient.next_appointment) }} · {{ patient.next_appointment.practitioner_name }}
          <span
            :class="[
              'h-4 px-1.5 rounded-full text-[10px] leading-4 font-medium',
              patient.next_appointment.confirmed
                ? 'bg-emerald-500/20 text-emerald-300 light:bg-emerald-100 light:text-emerald-700'
                : 'bg-amber-500/20 text-amber-300 light:bg-amber-100 light:text-amber-700'
            ]"
          >{{ patient.next_appointment.confirmed ? t('clinic.confirmed') : t('clinic.notConfirmed') }}</span>
        </template>
        <template v-else>{{ t('clinic.noneBooked') }}</template>
      </button>

      <span v-if="patient.balance" class="inline-flex items-center gap-1 font-medium" :class="due > 0 ? 'text-red-300 light:text-red-700' : credit > 0 ? 'text-emerald-300 light:text-emerald-700' : 'text-white/50 light:text-gray-500'">
        <Wallet class="h-3.5 w-3.5" />
        <template v-if="due > 0">{{ t('clinic.due', { amount: formatMoney(due, patient.balance.currency) }) }}</template>
        <template v-else-if="credit > 0">{{ t('clinic.credit', { amount: formatMoney(credit, patient.balance.currency) }) }}</template>
        <template v-else>{{ t('clinic.nothingDue') }}</template>
      </span>

      <span v-if="firstSession" class="inline-flex items-center gap-1 text-violet-300 light:text-violet-700 font-medium">
        <Layers class="h-3.5 w-3.5" />{{ firstSession.label }} {{ firstSession.done }}/{{ firstSession.total }}
      </span>
    </template>

    <!-- Not registered -->
    <template v-else>
      <span class="inline-flex items-center gap-1 h-6 px-2 rounded-full font-medium bg-rose-500/20 text-rose-200 light:bg-rose-100 light:text-rose-800">
        {{ t('clinic.notRegistered') }}
      </span>
      <button v-if="clinic.context.can_create" type="button" class="inline-flex items-center gap-1 font-medium text-emerald-300 light:text-emerald-700 hover:underline" @click="emit('open', 'new')">
        <UserPlus class="h-3.5 w-3.5" />{{ t('clinic.newPatient') }}
      </button>
      <button v-if="clinic.context.can_link" type="button" class="font-medium text-white/70 light:text-gray-700 hover:underline" @click="emit('open', 'link')">
        {{ t('clinic.addToExisting') }}
      </button>
    </template>

    <div class="ml-auto flex items-center gap-1">
      <button
        v-if="patient && clinic.context.can_book"
        type="button"
        class="inline-flex items-center gap-1 h-6 px-2 rounded-md font-medium bg-emerald-600 hover:bg-emerald-500 text-white"
        @click="emit('open', 'book')"
      >
        <CalendarPlus class="h-3.5 w-3.5" />{{ t('clinic.book') }}
      </button>
      <button
        type="button"
        class="inline-flex items-center gap-1 h-6 px-2 rounded-md font-medium bg-white/[0.08] hover:bg-white/[0.14] text-white/80 light:bg-gray-200 light:hover:bg-gray-300 light:text-gray-800"
        @click="emit('open', 'overview')"
      >
        <PanelRight class="h-3.5 w-3.5" />{{ t('clinic.details') }}
      </button>
    </div>
  </div>
</template>
