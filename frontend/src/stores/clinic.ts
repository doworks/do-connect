import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { clinicService } from '@/services/api'

export interface ClinicVisit {
  name: string
  date: string
  time?: string
  practitioner?: string
  practitioner_name: string
  color?: string
  type?: string
  status?: string
  confirmed?: boolean
}

export interface ClinicInvoice {
  name: string
  date: string
  outstanding: number
}

export interface ClinicPatient {
  name: string
  patient_name: string
  sex?: string
  age?: string
  file_number: string
  cpr_masked?: string
  language: string
  flags?: { label: string; level: 'danger' | 'warning' }[]
  next_appointment?: ClinicVisit | null
  last_visit?: ClinicVisit | null
  balance?: { due: number; credit: number; awaiting_insurance?: number; currency: string; invoices: ClinicInvoice[] } | null
  sessions?: { label: string; done: number; total: number }[]
  insurance?: { company: string; expires?: string } | null
  health_form?: { status: string; date?: string } | null
  open_tasks?: number
  notes?: { text: string; by: string }[]
}

export interface ClinicContext {
  phone: string
  patients: ClinicPatient[]
  can_create: boolean
  can_link: boolean
  can_book: boolean
  can_view_billing: boolean
}

const STORAGE_KEY = 'clinic_selected_patient'

function readSelected(): Record<string, string> {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
  } catch {
    return {}
  }
}

export const useClinicStore = defineStore('clinic', () => {
  const context = ref<ClinicContext | null>(null)
  const isLoading = ref(false)
  const selectedName = ref('')
  let requestedPhone = ''

  const selected = computed<ClinicPatient | null>(() => {
    const patients = context.value?.patients || []
    return patients.find(p => p.name === selectedName.value) || patients[0] || null
  })

  async function load(phone: string) {
    requestedPhone = phone
    isLoading.value = true
    try {
      const response = await clinicService.context(phone)
      // Staff may have moved to another chat while this was loading.
      if (requestedPhone !== phone) return
      context.value = response.data.data
      selectedName.value = readSelected()[phone] || ''
    } catch {
      if (requestedPhone === phone) context.value = null
    } finally {
      if (requestedPhone === phone) isLoading.value = false
    }
  }

  function clear() {
    requestedPhone = ''
    context.value = null
    selectedName.value = ''
  }

  // Which family member this chat is about, remembered per number.
  function select(patient: string) {
    selectedName.value = patient
    if (!context.value) return
    try {
      const saved = readSelected()
      saved[context.value.phone] = patient
      localStorage.setItem(STORAGE_KEY, JSON.stringify(saved))
    } catch { /* only a convenience */ }
  }

  function reload() {
    if (context.value) return load(context.value.phone)
  }

  return { context, isLoading, selected, load, clear, select, reload }
})
