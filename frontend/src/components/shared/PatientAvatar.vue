<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { X, Loader2 } from 'lucide-vue-next'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { cn } from '@/lib/utils'

// A patient's photo as an avatar. Once the photo has loaded, hovering shows it
// bigger beside the avatar and clicking opens it full screen (zoom with a
// click, close with Escape), like the pinned patient in the clinic system.
// Without a photo it is the initials, and clicks go to whatever is around it.
const props = defineProps<{
  src?: string
  name: string
  initials: string
  class?: string
  fallbackClass?: string
}>()

const { t } = useI18n()
const loaded = ref(false)
const peek = ref<{ top: number; left: number } | null>(null)
const open = ref(false)
const zoomed = ref(false)
const largeLoaded = ref(false)
const largeFailed = ref(false)
const root = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let peekTimer: ReturnType<typeof setTimeout> | undefined
let returnFocus: HTMLElement | null = null

const PEEK_SIZE = 224
const canHover = typeof window !== 'undefined' && !!window.matchMedia?.('(hover: hover)').matches

// The whole photo, not the avatar's square crop.
const largeSrc = computed(() => (props.src ? props.src + (props.src.includes('?') ? '&' : '?') + 'size=large' : ''))

watch(() => props.src, () => {
  loaded.value = false
  hidePeek()
})

function onStatus(status: string) {
  loaded.value = status === 'loaded'
}

function showPeekSoon() {
  if (!loaded.value || !canHover || open.value) return
  clearTimeout(peekTimer)
  peekTimer = setTimeout(() => {
    const rect = root.value?.getBoundingClientRect()
    if (!rect) return
    // Beside the avatar, on whichever side has room, and never off screen.
    const right = rect.right + 12
    const left = right + PEEK_SIZE <= window.innerWidth - 8 ? right : Math.max(8, rect.left - 12 - PEEK_SIZE)
    const top = Math.max(12, Math.min(rect.top - 8, window.innerHeight - PEEK_SIZE - 12))
    peek.value = { top, left }
  }, 280)
}

function hidePeek() {
  clearTimeout(peekTimer)
  peek.value = null
}

function openPhoto(event: Event) {
  if (!loaded.value) return
  // The photo, not the chat row or header behind it.
  event.preventDefault()
  event.stopPropagation()
  hidePeek()
  returnFocus = document.activeElement as HTMLElement | null
  zoomed.value = false
  largeLoaded.value = false
  largeFailed.value = false
  open.value = true
  nextTick(() => closeButton.value?.focus())
}

function closePhoto() {
  open.value = false
  returnFocus?.focus?.()
}

function onKey(event: KeyboardEvent) {
  if (open.value && event.key === 'Escape') {
    event.stopPropagation()
    closePhoto()
  }
}

watch(open, value => {
  if (value) window.addEventListener('keydown', onKey, true)
  else window.removeEventListener('keydown', onKey, true)
})

// Only a scroll that moves the avatar (the list it is in, or the page) hides
// the preview: the chat scrolling to a new message doesn't.
function onScroll(event: Event) {
  const target = event.target
  if (target instanceof Document || (target instanceof Node && root.value && target.contains(root.value))) hidePeek()
}

window.addEventListener('scroll', onScroll, true)
onBeforeUnmount(() => {
  clearTimeout(peekTimer)
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('keydown', onKey, true)
})
</script>

<template>
  <span
    ref="root"
    :class="cn('relative inline-flex shrink-0 rounded-full', loaded && 'cursor-zoom-in transition-transform hover:-translate-y-px', props.class)"
    :role="loaded ? 'button' : undefined"
    :tabindex="loaded ? 0 : undefined"
    :title="loaded ? t('patientPhoto.view') : undefined"
    :aria-label="loaded ? t('patientPhoto.view') : undefined"
    data-test="patient-avatar"
    @mouseenter="showPeekSoon"
    @mouseleave="hidePeek"
    @click="openPhoto"
    @keydown.enter="openPhoto"
    @keydown.space="openPhoto"
  >
    <Avatar class="h-full w-full">
      <AvatarImage v-if="src" :src="src" :alt="name" @loading-status-change="onStatus" />
      <AvatarFallback :class="fallbackClass">{{ initials }}</AvatarFallback>
    </Avatar>
  </span>

  <Teleport to="body">
    <div
      v-if="peek"
      class="fixed z-[60] overflow-hidden rounded-[22px] bg-white shadow-[0_28px_56px_-16px_rgba(15,23,42,0.5),0_0_0_1px_rgba(15,23,42,0.08)] pointer-events-none animate-in fade-in zoom-in-95 duration-150"
      :style="{ top: peek.top + 'px', left: peek.left + 'px', width: PEEK_SIZE + 'px', height: PEEK_SIZE + 'px' }"
      aria-hidden="true"
      data-test="photo-peek"
    >
      <img :src="src" alt="" class="block h-full w-full object-cover" />
    </div>

    <div
      v-if="open"
      class="fixed inset-0 z-[70] bg-black/85 animate-in fade-in duration-150"
      role="dialog"
      aria-modal="true"
      :aria-label="t('patientPhoto.title', { name })"
      data-test="photo-viewer"
      @click.self="closePhoto"
    >
      <div class="absolute top-0 inset-x-0 h-16 flex items-center justify-between px-4 sm:px-6 text-white">
        <p class="text-sm font-medium truncate" dir="auto">{{ name }}</p>
        <button
          ref="closeButton"
          type="button"
          class="h-10 w-10 rounded-full bg-white/10 hover:bg-white/20 flex items-center justify-center focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white"
          :aria-label="t('common.close')"
          data-test="photo-close"
          @click="closePhoto"
        >
          <X class="h-5 w-5" />
        </button>
      </div>
      <div class="absolute inset-x-4 sm:inset-x-[76px] top-16 bottom-7 flex items-center justify-center overflow-hidden" @click.self="closePhoto">
        <Loader2 v-if="!largeLoaded && !largeFailed" class="absolute h-7 w-7 animate-spin text-white/70" />
        <p v-if="largeFailed" class="text-sm text-white/70">{{ t('patientPhoto.failed') }}</p>
        <img
          v-show="largeLoaded"
          :src="largeSrc"
          :alt="name"
          draggable="false"
          data-test="photo-large"
          :class="['max-h-full max-w-full object-contain rounded-md shadow-[0_30px_80px_rgba(0,0,0,0.5)] select-none transition-transform duration-200',
                   'min-h-[min(62vh,560px)]', zoomed ? 'scale-[2.4] cursor-zoom-out' : 'cursor-zoom-in']"
          @load="largeLoaded = true"
          @error="largeFailed = true"
          @click="zoomed = !zoomed"
        />
      </div>
    </div>
  </Teleport>
</template>
