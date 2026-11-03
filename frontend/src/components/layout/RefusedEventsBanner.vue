<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { RouterLink } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { accountsService, type RefusedSummary } from '@/services/api'
import { AlertTriangle, X } from 'lucide-vue-next'

// Shown to admins when Meta's events for a number are being refused, so a wrong
// App Secret or a relay that drops the signature is noticed within minutes.

const POLL_MS = 5 * 60 * 1000
const DISMISS_KEY = 'refused-events-dismissed-at'

const authStore = useAuthStore()
const summaries = ref<RefusedSummary[]>([])
const accountNames = ref<Record<string, string>>({})
const dismissedAt = ref(readDismissed())
let timer: ReturnType<typeof setInterval> | undefined

function readDismissed(): string {
  try {
    return localStorage.getItem(DISMISS_KEY) || ''
  } catch {
    return ''
  }
}

const visible = computed(() => summaries.value.filter(s => !dismissedAt.value || s.latest_at > dismissedAt.value))
const first = computed(() => visible.value[0])

async function load() {
  if (!authStore.hasPermission('accounts', 'read')) return
  try {
    const response = await accountsService.refusedSummary()
    summaries.value = response.data.data?.accounts || []
    if (summaries.value.length && !Object.keys(accountNames.value).length) {
      const accounts = await accountsService.list()
      const list = accounts.data.data?.accounts || []
      accountNames.value = Object.fromEntries(list.map((a: { id: string; name: string }) => [a.id, a.name]))
    }
  } catch {
    // The banner is a helper; a failed check shouldn't bother anyone.
  }
}

function dismiss() {
  const latest = visible.value.reduce((max, s) => (s.latest_at > max ? s.latest_at : max), '')
  dismissedAt.value = latest
  try {
    localStorage.setItem(DISMISS_KEY, latest)
  } catch {
    // Private windows can refuse storage; it then reappears on the next check.
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, POLL_MS)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div
    v-if="first"
    role="alert"
    class="shrink-0 flex items-center gap-2 px-4 py-2 text-sm bg-red-950/70 text-red-200 border-b border-red-900 light:bg-red-50 light:text-red-800 light:border-red-200"
  >
    <AlertTriangle class="h-4 w-4 shrink-0" />
    <span class="min-w-0 flex-1">
      {{ $t('accounts.refusedBanner', {
        account: accountNames[first.account_id] || $t('accounts.refusedBannerNumber'),
        events: first.events,
        messages: first.messages,
      }) }}
    </span>
    <RouterLink
      :to="`/settings/accounts/${first.account_id}`"
      class="shrink-0 font-medium underline underline-offset-2"
    >{{ $t('accounts.refusedReview') }}</RouterLink>
    <button
      type="button"
      class="shrink-0 p-1 rounded hover:bg-red-900/60 light:hover:bg-red-100"
      :aria-label="$t('common.close')"
      @click="dismiss"
    >
      <X class="h-4 w-4" />
    </button>
  </div>
</template>
