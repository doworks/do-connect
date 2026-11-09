<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { BellRing, Building2, Loader2, Play, Volume1, Volume2, VolumeX } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Slider } from '@/components/ui/slider'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { organizationService, usersService } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useMessageSoundStore } from '@/stores/messageSound'
import { MESSAGE_SOUNDS, previewSound, type MessageSoundId, type MessageSoundWhen } from '@/lib/messageSound'
import { getErrorMessage } from '@/lib/api-utils'

// The message sound: for the whole clinic (Settings) or for the person signed
// in (Profile and Settings). A person's choice can follow the clinic's.
const props = defineProps<{ scope: 'clinic' | 'personal' }>()
const emit = defineEmits<{ saved: [] }>()

const { t } = useI18n()
const auth = useAuthStore()
const sounds = useMessageSoundStore()
const CLINIC = 'clinic'

const loading = ref(true)
const saving = ref(false)
const enabled = ref(true)
const sound = ref<string>(CLINIC)
// Null volume follows the clinic.
const volume = ref<number | null>(null)
const when = ref<string>(CLINIC)

const clinic = computed(() => sounds.clinic)
const isClinic = computed(() => props.scope === 'clinic')
const clinicOff = computed(() => !isClinic.value && !clinic.value.message_sound_enabled)
const disabled = computed(() => clinicOff.value || !enabled.value)

const effectiveSound = computed<MessageSoundId>(() => (sound.value === CLINIC ? clinic.value.message_sound : sound.value as MessageSoundId))
const effectiveVolume = computed(() => volume.value ?? clinic.value.message_sound_volume)
const sliderValue = computed({
  get: () => [effectiveVolume.value],
  set: (v: number[]) => { volume.value = v[0] },
})
const VolumeIcon = computed(() => (effectiveVolume.value === 0 ? VolumeX : effectiveVolume.value < 50 ? Volume1 : Volume2))

function soundName(id: string) {
  return t(`messageSound.sounds.${id}`)
}
function whenName(id: string) {
  return t(`messageSound.when.${id}`)
}

function fill() {
  if (isClinic.value) {
    enabled.value = clinic.value.message_sound_enabled
    sound.value = clinic.value.message_sound
    volume.value = clinic.value.message_sound_volume
    when.value = clinic.value.message_sound_when
    return
  }
  const mine = auth.userSettings
  const own = mine.message_sound
  enabled.value = !(own === 'off' || (own === undefined && mine.new_message_alerts === false))
  sound.value = own && own !== 'off' && (MESSAGE_SOUNDS as readonly string[]).includes(own) ? own : CLINIC
  volume.value = typeof mine.message_sound_volume === 'number' ? mine.message_sound_volume : null
  when.value = mine.message_sound_when === 'away' || mine.message_sound_when === 'always' ? mine.message_sound_when : CLINIC
}

onMounted(async () => {
  await sounds.load(true)
  fill()
  loading.value = false
})

function preview() {
  previewSound(effectiveSound.value, effectiveVolume.value)
}

// Arrow keys commit every step: play once the volume settles.
let previewTimer: ReturnType<typeof setTimeout> | undefined
function previewSoon() {
  clearTimeout(previewTimer)
  previewTimer = setTimeout(preview, 300)
}

async function save() {
  saving.value = true
  try {
    if (isClinic.value) {
      const value = {
        message_sound_enabled: enabled.value,
        message_sound: sound.value as MessageSoundId,
        message_sound_volume: volume.value ?? clinic.value.message_sound_volume,
        message_sound_when: when.value as MessageSoundWhen,
      }
      await organizationService.updateSettings(value)
      sounds.setClinic(value)
    } else {
      await usersService.updateMessageSound({
        message_sound: !enabled.value ? 'off' : sound.value === CLINIC ? '' : sound.value,
        message_sound_volume: volume.value,
        message_sound_when: when.value === CLINIC ? '' : when.value,
      })
      await auth.refreshUserData()
    }
    toast.success(t('messageSound.saved'))
    emit('saved')
  } catch (error) {
    toast.error(getErrorMessage(error, t('messageSound.saveFailed')))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="rounded-xl border border-white/[0.08] bg-white/[0.02] light:bg-white light:border-gray-200" :data-scope="scope">
    <div class="p-6 pb-3">
      <h3 class="text-lg font-semibold text-white light:text-gray-900 flex items-center gap-2">
        <component :is="isClinic ? Building2 : BellRing" class="h-5 w-5 text-amber-400 light:text-amber-600" />
        {{ isClinic ? t('messageSound.clinicTitle') : t('messageSound.title') }}
      </h3>
      <p class="text-sm text-white/40 light:text-gray-500">{{ isClinic ? t('messageSound.clinicDesc') : t('messageSound.desc') }}</p>
    </div>

    <div v-if="loading" class="p-6 pt-3"><Loader2 class="h-4 w-4 animate-spin text-white/40" /></div>
    <div v-else class="p-6 pt-3 space-y-5">
      <p v-if="clinicOff" class="rounded-lg px-3 py-2 text-sm bg-amber-500/10 text-amber-200 light:bg-amber-50 light:text-amber-800" data-test="clinic-off">
        {{ t('messageSound.clinicOff') }}
      </p>

      <div class="flex items-center justify-between gap-4">
        <div>
          <p class="font-medium text-white light:text-gray-900">{{ isClinic ? t('messageSound.clinicSwitch') : t('messageSound.switch') }}</p>
          <p class="text-sm text-white/40 light:text-gray-500">{{ isClinic ? t('messageSound.clinicSwitchDesc') : t('messageSound.switchDesc') }}</p>
        </div>
        <Switch id="message-sound-on" :checked="enabled" :disabled="clinicOff" @update:checked="enabled = $event" />
      </div>

      <div :class="['space-y-5 transition-opacity', disabled && 'opacity-50 grayscale pointer-events-none']" :aria-disabled="disabled">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="font-medium text-white light:text-gray-900">{{ t('messageSound.sound') }}</p>
          <div class="flex items-center gap-2">
            <Select v-model="sound" :disabled="disabled">
              <SelectTrigger id="message-sound-choice" class="w-56 bg-white/[0.04] border-white/[0.1] text-white/70 light:bg-white light:border-gray-200 light:text-gray-700">
                <SelectValue />
              </SelectTrigger>
              <SelectContent class="bg-[#141416] border-white/[0.08] light:bg-white light:border-gray-200">
                <SelectItem v-if="!isClinic" :value="CLINIC" class="text-white/70 focus:bg-white/[0.08] focus:text-white light:text-gray-700 light:focus:bg-gray-100">
                  {{ t('messageSound.clinicChoice', { name: soundName(clinic.message_sound) }) }}
                </SelectItem>
                <SelectItem v-for="id in MESSAGE_SOUNDS" :key="id" :value="id" class="text-white/70 focus:bg-white/[0.08] focus:text-white light:text-gray-700 light:focus:bg-gray-100">
                  {{ soundName(id) }}
                </SelectItem>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="sm"
              :aria-label="t('messageSound.play')"
              data-test="preview"
              class="bg-white/[0.04] border-white/[0.1] text-white/70 hover:bg-white/[0.08] hover:text-white light:bg-white light:border-gray-200 light:text-gray-700 light:hover:bg-gray-50"
              :disabled="disabled"
              @click="preview"
            >
              <Play class="h-4 w-4" />
            </Button>
          </div>
        </div>

        <div class="space-y-2">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <p class="font-medium text-white light:text-gray-900">{{ t('messageSound.volume') }}</p>
            <button
              v-if="!isClinic && volume !== null"
              type="button"
              data-test="clinic-volume"
              class="text-xs font-medium text-sky-300 light:text-sky-700 hover:underline"
              @click="volume = null"
            >{{ t('messageSound.useClinicVolume', { level: clinic.message_sound_volume }) }}</button>
            <span v-else-if="!isClinic" class="text-xs text-white/40 light:text-gray-500">{{ t('messageSound.clinicLevel') }}</span>
          </div>
          <div class="flex items-center gap-3">
            <component :is="VolumeIcon" class="h-4 w-4 shrink-0 text-white/50 light:text-gray-500" />
            <Slider
              v-model="sliderValue"
              :min="0"
              :max="100"
              :step="5"
              :disabled="disabled"
              :aria-label="t('messageSound.volume')"
              class="flex-1 max-w-sm"
              data-test="volume"
              @value-commit="previewSoon"
            />
            <span class="w-10 text-right text-sm tabular-nums text-white/70 light:text-gray-700" data-test="volume-label">{{ effectiveVolume }}%</span>
          </div>
        </div>

        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="font-medium text-white light:text-gray-900">{{ t('messageSound.whenTitle') }}</p>
            <p class="text-sm text-white/40 light:text-gray-500">{{ t('messageSound.whenDesc') }}</p>
          </div>
          <Select v-model="when" :disabled="disabled">
            <SelectTrigger id="message-sound-when" class="w-72 max-w-full bg-white/[0.04] border-white/[0.1] text-white/70 light:bg-white light:border-gray-200 light:text-gray-700">
              <SelectValue />
            </SelectTrigger>
            <SelectContent class="bg-[#141416] border-white/[0.08] light:bg-white light:border-gray-200">
              <SelectItem v-if="!isClinic" :value="CLINIC" class="text-white/70 focus:bg-white/[0.08] focus:text-white light:text-gray-700 light:focus:bg-gray-100">
                {{ t('messageSound.clinicChoice', { name: whenName(clinic.message_sound_when) }) }}
              </SelectItem>
              <SelectItem value="always" class="text-white/70 focus:bg-white/[0.08] focus:text-white light:text-gray-700 light:focus:bg-gray-100">{{ whenName('always') }}</SelectItem>
              <SelectItem value="away" class="text-white/70 focus:bg-white/[0.08] focus:text-white light:text-gray-700 light:focus:bg-gray-100">{{ whenName('away') }}</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      <div class="flex justify-end pt-1">
        <Button
          variant="outline"
          size="sm"
          data-test="save-sound"
          class="bg-white/[0.04] border-white/[0.1] text-white/70 hover:bg-white/[0.08] hover:text-white light:bg-white light:border-gray-200 light:text-gray-700 light:hover:bg-gray-50"
          :disabled="saving || clinicOff"
          @click="save"
        >
          <Loader2 v-if="saving" class="mr-2 h-4 w-4 animate-spin" />
          {{ t('settings.save') }}
        </Button>
      </div>
    </div>
  </div>
</template>
