import { inject, provide, ref, type InjectionKey, type Ref } from 'vue'
import { organizationService } from '@/services/api'

// Organization settings the chat screen needs, loaded once per visit to the
// chat and shared with its parts.
export interface ChatSettings {
  // Numbers staff type without a country code get this one (Settings, General).
  defaultCountryCode: Ref<string>
  // Messages can wait for the patient only once an unlock template is set up.
  canHoldMessages: Ref<boolean>
  load: () => void
}

const KEY: InjectionKey<ChatSettings> = Symbol('chatSettings')

function createChatSettings(): ChatSettings {
  const defaultCountryCode = ref('')
  const canHoldMessages = ref(false)
  function load() {
    organizationService.getSettings()
      .then(response => {
        const settings = response.data.data?.settings || {}
        defaultCountryCode.value = settings.default_country_code || ''
        canHoldMessages.value = !!(settings.unlock_template_en || settings.unlock_template_ar)
      })
      .catch(() => { /* typed numbers then need their country code */ })
  }
  return { defaultCountryCode, canHoldMessages, load }
}

export function provideChatSettings(): ChatSettings {
  const settings = createChatSettings()
  provide(KEY, settings)
  return settings
}

export function useChatSettings(): ChatSettings {
  return inject(KEY, createChatSettings, true)
}
