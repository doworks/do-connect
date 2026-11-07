<script setup lang="ts">
import { computed } from 'vue'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Label } from '@/components/ui/label'
import { PhoneIncoming, BellRing, Music, AlertTriangle } from 'lucide-vue-next'

// Settings, Calling: how incoming WhatsApp calls are answered. "answer" picks
// up at once and the caller hears the waiting sound while staff are rung;
// "ring" keeps the caller's phone ringing until someone picks up, for as long
// as Meta allows, then answers with the waiting sound or leaves a missed call.
const mode = defineModel<string>('mode', { required: true })
const noAnswer = defineModel<string>('noAnswer', { required: true })
const waitingSound = defineModel<string>('waitingSound', { required: true })
const props = defineProps<{ hasHoldMusic: boolean; hasRingback: boolean }>()

// Callers hear something whenever the call is answered before staff pick up.
const usesWaitingSound = computed(() => mode.value === 'answer' || noAnswer.value === 'hold')
const sound = computed({
  get: () => waitingSound.value || 'ringing',
  set: (value: string) => { waitingSound.value = value },
})
const soundMissing = computed(() =>
  sound.value === 'music' ? !props.hasHoldMusic && !props.hasRingback : !props.hasRingback && !props.hasHoldMusic)
const soundFallsBack = computed(() =>
  !soundMissing.value && (sound.value === 'music' ? !props.hasHoldMusic : !props.hasRingback))
</script>

<template>
  <div class="space-y-3">
    <div>
      <p class="font-medium text-white light:text-gray-900 flex items-center gap-2">
        <PhoneIncoming class="h-4 w-4 text-emerald-400 light:text-emerald-700" />{{ $t('callAnswer.title') }}
      </p>
      <p class="text-sm text-white/40 light:text-gray-500">{{ $t('callAnswer.subtitle') }}</p>
    </div>

    <RadioGroup v-model="mode" class="grid gap-2">
      <label
        for="call-mode-answer"
        :class="['flex gap-3 rounded-lg border p-3 cursor-pointer',
                 mode === 'answer' ? 'border-emerald-500/50 bg-emerald-500/[0.06] light:bg-emerald-50' : 'border-white/[0.08] light:border-gray-200']"
      >
        <RadioGroupItem id="call-mode-answer" value="answer" class="mt-0.5" />
        <span class="space-y-0.5">
          <span class="block font-medium text-white light:text-gray-900">{{ $t('callAnswer.answer') }}</span>
          <span class="block text-xs text-white/50 light:text-gray-500">{{ $t('callAnswer.answerHint') }}</span>
        </span>
      </label>

      <label
        for="call-mode-ring"
        :class="['flex gap-3 rounded-lg border p-3 cursor-pointer',
                 mode === 'ring' ? 'border-sky-500/50 bg-sky-500/[0.06] light:bg-sky-50' : 'border-white/[0.08] light:border-gray-200']"
      >
        <RadioGroupItem id="call-mode-ring" value="ring" class="mt-0.5" />
        <span class="space-y-0.5">
          <span class="block font-medium text-white light:text-gray-900">{{ $t('callAnswer.ring') }}</span>
          <span class="block text-xs text-white/50 light:text-gray-500">{{ $t('callAnswer.ringHint') }}</span>
        </span>
      </label>
    </RadioGroup>

    <div v-if="mode === 'ring'" class="ml-7 space-y-2">
      <p class="text-sm text-white/70 light:text-gray-700">{{ $t('callAnswer.noAnswerTitle') }}</p>
      <RadioGroup v-model="noAnswer" class="grid gap-1.5">
        <div class="flex items-center gap-2">
          <RadioGroupItem id="no-answer-hold" value="hold" />
          <Label for="no-answer-hold" class="font-normal cursor-pointer">{{ $t('callAnswer.noAnswerHold') }}</Label>
        </div>
        <div class="flex items-center gap-2">
          <RadioGroupItem id="no-answer-missed" value="missed" />
          <Label for="no-answer-missed" class="font-normal cursor-pointer">{{ $t('callAnswer.noAnswerMissed') }}</Label>
        </div>
      </RadioGroup>
      <p class="text-xs text-white/40 light:text-gray-500">{{ $t('callAnswer.noMenus') }}</p>
    </div>

    <div v-if="usesWaitingSound" class="ml-7 space-y-2">
      <p class="text-sm text-white/70 light:text-gray-700">{{ $t('callAnswer.soundTitle') }}</p>
      <RadioGroup v-model="sound" class="flex flex-wrap gap-2">
        <label
          for="sound-ringing"
          :class="['flex items-center gap-2 rounded-md border px-3 py-1.5 cursor-pointer text-sm',
                   sound === 'ringing' ? 'border-violet-500/50 bg-violet-500/[0.08] text-white light:bg-violet-50 light:text-gray-900' : 'border-white/[0.08] text-white/70 light:border-gray-200 light:text-gray-700']"
        >
          <RadioGroupItem id="sound-ringing" value="ringing" />
          <BellRing class="h-4 w-4 text-violet-400 light:text-violet-600" />{{ $t('callAnswer.soundRinging') }}
        </label>
        <label
          for="sound-music"
          :class="['flex items-center gap-2 rounded-md border px-3 py-1.5 cursor-pointer text-sm',
                   sound === 'music' ? 'border-amber-500/50 bg-amber-500/[0.08] text-white light:bg-amber-50 light:text-gray-900' : 'border-white/[0.08] text-white/70 light:border-gray-200 light:text-gray-700']"
        >
          <RadioGroupItem id="sound-music" value="music" />
          <Music class="h-4 w-4 text-amber-400 light:text-amber-600" />{{ $t('callAnswer.soundMusic') }}
        </label>
      </RadioGroup>
      <p v-if="soundMissing" class="flex items-center gap-1.5 text-xs text-amber-300 light:text-amber-700">
        <AlertTriangle class="h-3.5 w-3.5 shrink-0" />{{ $t('callAnswer.soundMissing') }}
      </p>
      <p v-else-if="soundFallsBack" class="text-xs text-white/40 light:text-gray-500">
        {{ sound === 'music' ? $t('callAnswer.noMusicYet') : $t('callAnswer.noRingingYet') }}
      </p>
    </div>
  </div>
</template>
