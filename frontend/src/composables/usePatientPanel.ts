import { ref, watch } from 'vue'
import { useContactsStore } from '@/stores/contacts'
import { useClinicStore } from '@/stores/clinic'

// The clinic record for the open chat (PatientStrip) and the side panel with
// the patient's details and clinic actions (PatientPanel). Nothing shows when
// no clinic system is set up.
export function usePatientPanel() {
  const contactsStore = useContactsStore()
  const clinic = useClinicStore()
  const patientPanelView = ref('overview')
  const isPatientPanelOpen = ref(false)

  watch(() => contactsStore.currentContact?.phone_number, (phone) => {
    if (phone) clinic.load(phone)
    else clinic.clear()
  }, { immediate: true })

  function openPatientPanel(view: string) {
    patientPanelView.value = view
    isPatientPanelOpen.value = true
  }

  return { patientPanelView, isPatientPanelOpen, openPatientPanel }
}
