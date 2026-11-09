import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { organizationService } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { CLINIC_DEFAULT, MESSAGE_SOUNDS, resolveSound, type ClinicSound, type MessageSoundId } from '@/lib/messageSound'

// Fresh enough: a clinic change reaches open tabs when they're next in front.
const STALE_MS = 5 * 60 * 1000

export function clinicSoundFrom(settings: any): ClinicSound {
  const s = settings || {}
  return {
    message_sound_enabled: s.message_sound_enabled !== false,
    message_sound: (MESSAGE_SOUNDS as readonly string[]).includes(s.message_sound) ? s.message_sound as MessageSoundId : CLINIC_DEFAULT.message_sound,
    message_sound_volume: typeof s.message_sound_volume === 'number' ? s.message_sound_volume : CLINIC_DEFAULT.message_sound_volume,
    message_sound_when: s.message_sound_when === 'away' ? 'away' : s.message_sound_when === 'always' ? 'always' : CLINIC_DEFAULT.message_sound_when,
  }
}

export const useMessageSoundStore = defineStore('messageSound', () => {
  const clinic = ref<ClinicSound>({ ...CLINIC_DEFAULT })
  let loadedAt = 0

  async function load(force = false) {
    if (!force && Date.now() - loadedAt < STALE_MS) return
    loadedAt = Date.now()
    try {
      const response = await organizationService.getSettings()
      const data = response.data.data || response.data
      clinic.value = clinicSoundFrom(data?.settings)
    } catch {
      loadedAt = 0
    }
  }

  function setClinic(value: ClinicSound) {
    clinic.value = { ...value }
    loadedAt = Date.now()
  }

  const resolved = computed(() => resolveSound(clinic.value, useAuthStore().userSettings as any))

  return { clinic, resolved, load, setClinic }
})
