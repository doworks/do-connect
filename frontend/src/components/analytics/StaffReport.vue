<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Skeleton } from '@/components/ui/skeleton'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { ErrorState } from '@/components/shared'
import { agentAnalyticsService } from '@/services/api'
import {
  barHeights,
  dayLabel,
  formatDuration,
  hasActivity,
  onTargetPercent,
  replyTone,
  shareTone,
  sortRows,
  waitingSecs,
  type SortKey,
  type StaffDay,
  type StaffReport,
  type StaffRow,
  type Tone,
} from '@/lib/staffReport'
import {
  Bot,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Hourglass,
  Inbox,
  MessageSquare,
  MessagesSquare,
  Timer,
  Target,
  Users,
} from 'lucide-vue-next'

const props = defineProps<{
  from: string
  to: string
  /** Show only this person, or everyone with 'all'. */
  agentId: string
}>()

const { t, locale } = useI18n()

const report = ref<StaffReport | null>(null)
const isLoading = ref(true)
const error = ref<string | null>(null)
const now = ref(new Date())
let clock: ReturnType<typeof setInterval> | undefined

const fetchReport = async () => {
  isLoading.value = true
  error.value = null
  try {
    const response = await agentAnalyticsService.getStaffReport({ from: props.from, to: props.to })
    report.value = response.data.data || response.data
    now.value = new Date()
  } catch (err) {
    console.error('Failed to load staff report:', err)
    error.value = t('staffReport.loadError')
    report.value = null
  } finally {
    isLoading.value = false
  }
}

watch(() => [props.from, props.to], fetchReport)
onMounted(() => {
  fetchReport()
  // Waiting ages tick on while the page is open.
  clock = setInterval(() => { now.value = new Date() }, 60_000)
})
onUnmounted(() => clearInterval(clock))

const target = computed(() => report.value?.target_secs ?? 900)
const slow = computed(() => report.value?.slow_secs ?? 3600)
const tone = (secs: number | null | undefined): Tone => replyTone(secs, target.value, slow.value)

// Each tone means the same everywhere: green within target, amber slow, red very slow.
const toneText: Record<Tone, string> = {
  good: 'text-emerald-400 light:text-emerald-600',
  slow: 'text-amber-400 light:text-amber-600',
  'very-slow': 'text-rose-400 light:text-rose-600',
  none: 'text-white/30 light:text-gray-400',
}
const toneChip: Record<Tone, string> = {
  good: 'bg-emerald-500/15 text-emerald-300 ring-emerald-500/30 light:bg-emerald-50 light:text-emerald-700 light:ring-emerald-200',
  slow: 'bg-amber-500/15 text-amber-300 ring-amber-500/30 light:bg-amber-50 light:text-amber-700 light:ring-amber-200',
  'very-slow': 'bg-rose-500/15 text-rose-300 ring-rose-500/30 light:bg-rose-50 light:text-rose-700 light:ring-rose-200',
  none: 'bg-transparent text-white/25 ring-transparent light:text-gray-400',
}
// Days with chats but no new waits answered are blue: work, nothing to time.
const toneBar: Record<Tone, string> = {
  good: 'bg-emerald-500',
  slow: 'bg-amber-500',
  'very-slow': 'bg-rose-500',
  none: 'bg-sky-500/80',
}

const sortKey = ref<SortKey>('chats_answered')
const sortDir = ref<'asc' | 'desc'>('desc')
const setSort = (key: SortKey) => {
  if (sortKey.value === key) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortKey.value = key
    // Fastest first for times, biggest first for counts, A to Z for names.
    sortDir.value = key === 'name' || key.endsWith('_secs') ? 'asc' : 'desc'
  }
}

const people = computed(() => {
  if (!report.value) return []
  let rows = report.value.people
  if (props.agentId !== 'all') rows = rows.filter(r => r.user_id === props.agentId)
  const sorted = sortRows(rows, sortKey.value, sortDir.value)
  // People with nothing in the range stay listed, after everyone who worked.
  return [...sorted.filter(hasActivity), ...sorted.filter(r => !hasActivity(r))]
})

const columns: { key: SortKey; label: string; hint: string }[] = [
  { key: 'chats_answered', label: 'staffReport.chatsAnswered', hint: 'staffReport.chatsAnsweredHint' },
  { key: 'messages_sent', label: 'staffReport.messagesSent', hint: 'staffReport.messagesSentHint' },
  { key: 'first_replies', label: 'staffReport.firstReplies', hint: 'staffReport.firstRepliesHint' },
  { key: 'median_first_reply_secs', label: 'staffReport.median', hint: 'staffReport.medianHint' },
  { key: 'p90_first_reply_secs', label: 'staffReport.p90', hint: 'staffReport.p90Hint' },
  { key: 'on_target_percent', label: 'staffReport.onTarget', hint: 'staffReport.onTargetHint' },
  { key: 'waiting_now', label: 'staffReport.waitingNow', hint: 'staffReport.waitingNowHint' },
  { key: 'owned_chats', label: 'staffReport.owns', hint: 'staffReport.ownsHint' },
]

const waitTone = (row: StaffRow): Tone => (row.waiting_now ? tone(waitingSecs(row.oldest_waiting_since, now.value)) : 'none')
const waitAge = (row: StaffRow) => formatDuration(waitingSecs(row.oldest_waiting_since, now.value))
const share = (row: { first_replies: number; on_target: number }) => onTargetPercent(row)

const initials = (name: string) =>
  name.split(/\s+/).filter(Boolean).slice(0, 2).map(p => p[0]?.toUpperCase()).join('') || '?'

// One fixed hue per person, from their id, so a person keeps their colour across visits.
const avatarHues = ['bg-sky-500', 'bg-violet-500', 'bg-teal-500', 'bg-fuchsia-500', 'bg-indigo-500', 'bg-cyan-600', 'bg-pink-500', 'bg-blue-600']
const avatarHue = (id?: string) => {
  if (!id) return 'bg-gray-500'
  let h = 0
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return avatarHues[h % avatarHues.length]
}

const sparkHeights = (daily: StaffDay[], metric: 'chats_answered' | 'messages_sent' = 'chats_answered') =>
  barHeights(daily.map(d => d[metric]))
const sparkTitle = (d: StaffDay) =>
  t('staffReport.sparkTitle', {
    day: dayLabel(d.date, locale.value, true),
    chats: d.chats_answered,
    messages: d.messages_sent,
    median: formatDuration(d.median_first_reply_secs),
  })

const rangeLabel = computed(() => {
  if (!report.value) return ''
  const { from, to } = report.value
  return from === to ? dayLabel(from, locale.value, true) : `${dayLabel(from, locale.value)} – ${dayLabel(to, locale.value)}`
})

// Drill-down
type Selected = { kind: 'person' | 'team'; row: StaffRow } | { kind: 'automatic' }
const selected = ref<Selected | null>(null)
const dialogOpen = computed({
  get: () => selected.value !== null,
  set: (open: boolean) => { if (!open) selected.value = null },
})
const selectedDays = computed<StaffDay[]>(() => {
  if (!selected.value) return []
  const days = selected.value.kind === 'automatic' ? report.value?.automatic.daily ?? [] : selected.value.row.daily
  return [...days].reverse()
})
const busiestDay = computed(() => Math.max(1, ...selectedDays.value.map(d => d.chats_answered)))
const sourceName = (s: { kind: string; label?: string }) =>
  s.kind === 'labelled' ? s.label || t('staffReport.automatic') : t(`staffReport.source.${s.kind}`)
</script>

<template>
  <section class="rounded-xl border border-white/[0.08] bg-white/[0.02] light:bg-white light:border-gray-200" data-testid="staff-report">
    <!-- Header -->
    <div class="flex flex-wrap items-start justify-between gap-3 px-5 pt-4 pb-3 border-b border-white/[0.06] light:border-gray-100">
      <div class="min-w-0">
        <h2 class="text-base font-semibold text-white light:text-gray-900 flex items-center gap-2">
          <Users class="h-4 w-4 text-sky-400 light:text-sky-600" />
          {{ $t('staffReport.title') }}
          <span v-if="report" class="text-xs font-normal text-white/40 light:text-gray-500">· {{ rangeLabel }}</span>
        </h2>
        <p class="text-xs text-white/45 light:text-gray-500 mt-0.5">
          {{ $t('staffReport.subtitle') }}
          <span v-if="report"> {{ $t('staffReport.daysIn', { zone: report.timezone }) }}</span>
        </p>
      </div>
      <!-- What the colours mean -->
      <div class="flex flex-wrap items-center gap-1.5 text-[11px]">
        <span class="text-white/40 light:text-gray-500 mr-0.5">{{ $t('staffReport.firstReplyLegend') }}</span>
        <span :class="['inline-flex items-center gap-1 rounded-full px-2 py-0.5 ring-1', toneChip.good]">
          <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />{{ $t('staffReport.legendGood', { time: formatDuration(target) }) }}
        </span>
        <span :class="['inline-flex items-center gap-1 rounded-full px-2 py-0.5 ring-1', toneChip.slow]">
          <span class="h-1.5 w-1.5 rounded-full bg-amber-500" />{{ $t('staffReport.legendSlow', { time: formatDuration(slow) }) }}
        </span>
        <span :class="['inline-flex items-center gap-1 rounded-full px-2 py-0.5 ring-1', toneChip['very-slow']]">
          <span class="h-1.5 w-1.5 rounded-full bg-rose-500" />{{ $t('staffReport.legendVerySlow', { time: formatDuration(slow) }) }}
        </span>
      </div>
    </div>

    <div v-if="error && !isLoading" class="p-5">
      <ErrorState :title="$t('common.loadErrorTitle')" :description="error" :retry-label="$t('common.retry')" @retry="fetchReport" />
    </div>

    <template v-else>
      <!-- Team at a glance -->
      <div class="grid grid-cols-2 sm:grid-cols-3 xl:grid-cols-6 gap-2 px-5 py-3">
        <template v-if="isLoading || !report">
          <Skeleton v-for="i in 6" :key="i" class="h-[68px] rounded-lg bg-white/[0.06] light:bg-gray-100" />
        </template>
        <template v-else>
          <div class="rounded-lg border border-sky-500/20 bg-sky-500/[0.07] px-3 py-2 light:bg-sky-50 light:border-sky-100">
            <div class="flex items-center gap-1.5 text-[11px] font-medium text-sky-300 light:text-sky-700"><MessagesSquare class="h-3.5 w-3.5" />{{ $t('staffReport.chatsAnswered') }}</div>
            <div class="text-2xl font-bold text-white light:text-gray-900 leading-tight">{{ report.team.chats_answered }}</div>
            <div class="text-[11px] text-white/40 light:text-gray-500">{{ $t('staffReport.byPeople') }}</div>
          </div>
          <div class="rounded-lg border border-indigo-500/20 bg-indigo-500/[0.07] px-3 py-2 light:bg-indigo-50 light:border-indigo-100">
            <div class="flex items-center gap-1.5 text-[11px] font-medium text-indigo-300 light:text-indigo-700"><MessageSquare class="h-3.5 w-3.5" />{{ $t('staffReport.messagesSent') }}</div>
            <div class="text-2xl font-bold text-white light:text-gray-900 leading-tight">{{ report.team.messages_sent }}</div>
            <div class="text-[11px] text-white/40 light:text-gray-500">{{ $t('staffReport.firstRepliesCount', { count: report.team.first_replies }) }}</div>
          </div>
          <div :class="['rounded-lg border px-3 py-2 ring-1 ring-inset', toneChip[tone(report.team.median_first_reply_secs)], 'border-white/[0.06] light:border-gray-100']">
            <div class="flex items-center gap-1.5 text-[11px] font-medium"><Timer class="h-3.5 w-3.5" />{{ $t('staffReport.medianFirstReply') }}</div>
            <div class="text-2xl font-bold leading-tight">{{ formatDuration(report.team.median_first_reply_secs) }}</div>
            <div class="text-[11px] opacity-80">{{ $t('staffReport.p90Is', { time: formatDuration(report.team.p90_first_reply_secs) }) }}</div>
          </div>
          <div :class="['rounded-lg border px-3 py-2 ring-1 ring-inset', toneChip[shareTone(share(report.team))], 'border-white/[0.06] light:border-gray-100']">
            <div class="flex items-center gap-1.5 text-[11px] font-medium"><Target class="h-3.5 w-3.5" />{{ $t('staffReport.onTargetLong', { time: formatDuration(target) }) }}</div>
            <div class="text-2xl font-bold leading-tight">{{ share(report.team) == null ? '—' : `${share(report.team)}%` }}</div>
            <div class="text-[11px] opacity-80">{{ $t('staffReport.ofFirstReplies', { count: report.team.on_target, total: report.team.first_replies }) }}</div>
          </div>
          <div :class="['rounded-lg border px-3 py-2 ring-1 ring-inset', report.team.waiting_now ? toneChip[waitTone(report.team)] : toneChip.good, 'border-white/[0.06] light:border-gray-100']">
            <div class="flex items-center gap-1.5 text-[11px] font-medium"><Hourglass class="h-3.5 w-3.5" />{{ $t('staffReport.waitingNow') }}</div>
            <div class="text-2xl font-bold leading-tight">{{ report.team.waiting_now }}</div>
            <div class="text-[11px] opacity-80">
              {{ report.team.waiting_now ? $t('staffReport.oldestWaiting', { time: waitAge(report.team) }) : $t('staffReport.nobodyWaiting') }}
            </div>
          </div>
          <button
            type="button"
            class="text-left rounded-lg border border-violet-500/20 bg-violet-500/[0.07] px-3 py-2 hover:bg-violet-500/[0.12] transition-colors light:bg-violet-50 light:border-violet-100 light:hover:bg-violet-100"
            @click="selected = { kind: 'automatic' }"
          >
            <div class="flex items-center gap-1.5 text-[11px] font-medium text-violet-300 light:text-violet-700"><Bot class="h-3.5 w-3.5" />{{ $t('staffReport.automatic') }}</div>
            <div class="text-2xl font-bold text-white light:text-gray-900 leading-tight">{{ report.automatic.messages_sent }}</div>
            <div class="text-[11px] text-white/40 light:text-gray-500">{{ $t('staffReport.notCounted') }}</div>
          </button>
        </template>
      </div>

      <!-- One row per person -->
      <div class="overflow-x-auto px-2 pb-3">
        <table class="w-full text-sm border-separate border-spacing-0">
          <thead>
            <tr class="text-[11px] uppercase tracking-wide text-white/40 light:text-gray-500">
              <th class="text-left font-medium px-3 py-2 border-b border-white/[0.06] light:border-gray-100">
                <button type="button" class="inline-flex items-center gap-1 hover:text-white light:hover:text-gray-900" @click="setSort('name')">
                  {{ $t('staffReport.person') }}
                  <component :is="sortDir === 'asc' ? ChevronUp : ChevronDown" v-if="sortKey === 'name'" class="h-3 w-3" />
                </button>
              </th>
              <th
                v-for="col in columns"
                :key="col.key"
                class="text-right font-medium px-2 py-2 border-b border-white/[0.06] light:border-gray-100 whitespace-nowrap"
                :title="$t(col.hint, { time: formatDuration(target) })"
              >
                <button type="button" class="inline-flex items-center gap-0.5 hover:text-white light:hover:text-gray-900" @click="setSort(col.key)">
                  <component :is="sortDir === 'asc' ? ChevronUp : ChevronDown" v-if="sortKey === col.key" class="h-3 w-3" />
                  {{ $t(col.label, { time: formatDuration(target) }) }}
                </button>
              </th>
              <th class="text-left font-medium px-3 py-2 border-b border-white/[0.06] light:border-gray-100" :title="$t('staffReport.trendHint')">
                {{ $t('staffReport.trend') }}
              </th>
              <th class="w-6 border-b border-white/[0.06] light:border-gray-100" />
            </tr>
          </thead>

          <tbody v-if="isLoading || !report">
            <tr v-for="i in 4" :key="i">
              <td colspan="11" class="px-3 py-2"><Skeleton class="h-7 w-full bg-white/[0.06] light:bg-gray-100" /></td>
            </tr>
          </tbody>

          <tbody v-else>
            <tr v-if="people.length === 0">
              <td colspan="11" class="px-3 py-6 text-center text-white/40 light:text-gray-500">{{ $t('staffReport.nobody') }}</td>
            </tr>
            <tr
              v-for="row in people"
              :key="row.user_id"
              :class="[
                'group cursor-pointer transition-colors hover:bg-white/[0.04] light:hover:bg-gray-50',
                hasActivity(row) ? '' : 'opacity-50',
              ]"
              @click="selected = { kind: 'person', row }"
            >
              <td class="px-3 py-1.5 border-b border-white/[0.04] light:border-gray-50">
                <div class="flex items-center gap-2 min-w-[150px]">
                  <span class="relative shrink-0">
                    <span :class="['h-7 w-7 rounded-full flex items-center justify-center text-[11px] font-semibold text-white', avatarHue(row.user_id)]">{{ initials(row.name) }}</span>
                    <span
                      v-if="row.is_active"
                      :class="['absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-[#0f0f10] light:ring-white', row.is_available ? 'bg-emerald-500' : 'bg-amber-500']"
                      :title="row.is_available ? $t('staffReport.available') : $t('staffReport.away')"
                    />
                  </span>
                  <span class="truncate font-medium text-white/90 light:text-gray-900">{{ row.name || $t('staffReport.unknown') }}</span>
                  <span v-if="!row.is_active" class="text-[10px] rounded px-1 py-px bg-white/[0.06] text-white/50 light:bg-gray-100 light:text-gray-500">{{ $t('staffReport.left') }}</span>
                </div>
              </td>
              <td class="px-2 py-1.5 text-right tabular-nums font-semibold text-white light:text-gray-900 border-b border-white/[0.04] light:border-gray-50">{{ row.chats_answered }}</td>
              <td class="px-2 py-1.5 text-right tabular-nums text-white/70 light:text-gray-700 border-b border-white/[0.04] light:border-gray-50">{{ row.messages_sent }}</td>
              <td class="px-2 py-1.5 text-right tabular-nums text-white/70 light:text-gray-700 border-b border-white/[0.04] light:border-gray-50">{{ row.first_replies }}</td>
              <td class="px-2 py-1.5 text-right border-b border-white/[0.04] light:border-gray-50">
                <span :class="['inline-block min-w-[48px] text-center rounded-md px-1.5 py-0.5 text-xs font-semibold tabular-nums ring-1', toneChip[tone(row.median_first_reply_secs)]]">
                  {{ formatDuration(row.median_first_reply_secs) }}
                </span>
              </td>
              <td class="px-2 py-1.5 text-right border-b border-white/[0.04] light:border-gray-50">
                <span :class="['inline-block min-w-[48px] text-center rounded-md px-1.5 py-0.5 text-xs font-semibold tabular-nums ring-1', toneChip[tone(row.p90_first_reply_secs)]]">
                  {{ formatDuration(row.p90_first_reply_secs) }}
                </span>
              </td>
              <td :class="['px-2 py-1.5 text-right tabular-nums text-xs font-semibold border-b border-white/[0.04] light:border-gray-50', toneText[shareTone(share(row))]]">
                {{ share(row) == null ? '—' : `${share(row)}%` }}
              </td>
              <td class="px-2 py-1.5 text-right border-b border-white/[0.04] light:border-gray-50 whitespace-nowrap">
                <template v-if="row.waiting_now">
                  <span :class="['tabular-nums font-semibold', toneText[waitTone(row)]]">{{ row.waiting_now }}</span>
                  <span :class="['ml-1 text-[11px]', toneText[waitTone(row)]]">· {{ waitAge(row) }}</span>
                </template>
                <span v-else class="text-white/25 light:text-gray-300">0</span>
              </td>
              <td class="px-2 py-1.5 text-right tabular-nums text-white/70 light:text-gray-700 border-b border-white/[0.04] light:border-gray-50">{{ row.owned_chats }}</td>
              <td class="px-3 py-1.5 border-b border-white/[0.04] light:border-gray-50">
                <div class="flex items-end gap-px h-6 w-[132px]" :aria-label="$t('staffReport.trend')">
                  <span
                    v-for="(h, i) in sparkHeights(row.daily)"
                    :key="row.daily[i].date"
                    :title="sparkTitle(row.daily[i])"
                    :class="['flex-1 rounded-t-[2px] min-w-[2px]', h > 0 ? toneBar[tone(row.daily[i].median_first_reply_secs)] : 'bg-white/[0.08] light:bg-gray-200']"
                    :style="{ height: h > 0 ? `${Math.max(12, h * 100)}%` : '2px' }"
                  />
                </div>
              </td>
              <td class="pr-2 border-b border-white/[0.04] light:border-gray-50">
                <ChevronRight class="h-4 w-4 text-white/20 group-hover:text-white/60 light:text-gray-300 light:group-hover:text-gray-600" />
              </td>
            </tr>

            <!-- Chats nobody owns yet -->
            <tr v-if="agentId === 'all' && (report.unassigned.waiting_now || report.unassigned.owned_chats)" class="bg-white/[0.015] light:bg-gray-50/60">
              <td class="px-3 py-1.5 border-b border-white/[0.04] light:border-gray-100">
                <div class="flex items-center gap-2">
                  <span class="h-7 w-7 rounded-full flex items-center justify-center bg-white/[0.06] light:bg-gray-200"><Inbox class="h-3.5 w-3.5 text-white/60 light:text-gray-600" /></span>
                  <span class="font-medium text-white/70 light:text-gray-700">{{ $t('staffReport.unassigned') }}</span>
                </div>
              </td>
              <td colspan="6" class="px-2 py-1.5 text-[11px] text-white/35 light:text-gray-400 border-b border-white/[0.04] light:border-gray-100">{{ $t('staffReport.unassignedHint') }}</td>
              <td class="px-2 py-1.5 text-right border-b border-white/[0.04] light:border-gray-100 whitespace-nowrap">
                <template v-if="report.unassigned.waiting_now">
                  <span :class="['tabular-nums font-semibold', toneText[waitTone(report.unassigned)]]">{{ report.unassigned.waiting_now }}</span>
                  <span :class="['ml-1 text-[11px]', toneText[waitTone(report.unassigned)]]">· {{ waitAge(report.unassigned) }}</span>
                </template>
                <span v-else class="text-white/25 light:text-gray-300">0</span>
              </td>
              <td class="px-2 py-1.5 text-right tabular-nums text-white/70 light:text-gray-700 border-b border-white/[0.04] light:border-gray-100">{{ report.unassigned.owned_chats }}</td>
              <td colspan="2" class="border-b border-white/[0.04] light:border-gray-100" />
            </tr>

            <!-- Automatic: never counted as anyone's work -->
            <tr
              v-if="agentId === 'all'"
              class="group cursor-pointer bg-violet-500/[0.04] hover:bg-violet-500/[0.08] light:bg-violet-50/60 light:hover:bg-violet-50"
              @click="selected = { kind: 'automatic' }"
            >
              <td class="px-3 py-1.5 border-b border-white/[0.04] light:border-gray-100">
                <div class="flex items-center gap-2">
                  <span class="h-7 w-7 rounded-full flex items-center justify-center bg-violet-500/20 light:bg-violet-100"><Bot class="h-3.5 w-3.5 text-violet-300 light:text-violet-700" /></span>
                  <span class="font-medium text-violet-200 light:text-violet-800">{{ $t('staffReport.automatic') }}</span>
                </div>
              </td>
              <td class="px-2 py-1.5 text-right tabular-nums text-violet-200/80 light:text-violet-800 border-b border-white/[0.04] light:border-gray-100" :title="$t('staffReport.chatsReached')">{{ report.automatic.chats_reached }}</td>
              <td class="px-2 py-1.5 text-right tabular-nums text-violet-200/80 light:text-violet-800 border-b border-white/[0.04] light:border-gray-100">{{ report.automatic.messages_sent }}</td>
              <td colspan="6" class="px-2 py-1.5 text-[11px] text-white/35 light:text-gray-500 border-b border-white/[0.04] light:border-gray-100">{{ $t('staffReport.automaticHint') }}</td>
              <td class="px-3 py-1.5 border-b border-white/[0.04] light:border-gray-100">
                <div class="flex items-end gap-px h-6 w-[132px]">
                  <span
                    v-for="(h, i) in sparkHeights(report.automatic.daily, 'messages_sent')"
                    :key="report.automatic.daily[i].date"
                    :title="`${dayLabel(report.automatic.daily[i].date, locale, true)}: ${report.automatic.daily[i].messages_sent}`"
                    :class="['flex-1 rounded-t-[2px] min-w-[2px]', h > 0 ? 'bg-violet-500/80' : 'bg-white/[0.08] light:bg-gray-200']"
                    :style="{ height: h > 0 ? `${Math.max(12, h * 100)}%` : '2px' }"
                  />
                </div>
              </td>
              <td class="pr-2 border-b border-white/[0.04] light:border-gray-100">
                <ChevronRight class="h-4 w-4 text-violet-300/40 group-hover:text-violet-300 light:text-violet-300 light:group-hover:text-violet-600" />
              </td>
            </tr>

            <!-- Team totals -->
            <tr
              v-if="agentId === 'all'"
              class="group cursor-pointer font-semibold hover:bg-white/[0.04] light:hover:bg-gray-50"
              @click="selected = { kind: 'team', row: report.team }"
            >
              <td class="px-3 py-2 text-white light:text-gray-900">{{ $t('staffReport.team') }}</td>
              <td class="px-2 py-2 text-right tabular-nums text-white light:text-gray-900">{{ report.team.chats_answered }}</td>
              <td class="px-2 py-2 text-right tabular-nums text-white/80 light:text-gray-800">{{ report.team.messages_sent }}</td>
              <td class="px-2 py-2 text-right tabular-nums text-white/80 light:text-gray-800">{{ report.team.first_replies }}</td>
              <td class="px-2 py-2 text-right">
                <span :class="['inline-block min-w-[48px] text-center rounded-md px-1.5 py-0.5 text-xs tabular-nums ring-1', toneChip[tone(report.team.median_first_reply_secs)]]">{{ formatDuration(report.team.median_first_reply_secs) }}</span>
              </td>
              <td class="px-2 py-2 text-right">
                <span :class="['inline-block min-w-[48px] text-center rounded-md px-1.5 py-0.5 text-xs tabular-nums ring-1', toneChip[tone(report.team.p90_first_reply_secs)]]">{{ formatDuration(report.team.p90_first_reply_secs) }}</span>
              </td>
              <td :class="['px-2 py-2 text-right tabular-nums text-xs', toneText[shareTone(share(report.team))]]">{{ share(report.team) == null ? '—' : `${share(report.team)}%` }}</td>
              <td :class="['px-2 py-2 text-right tabular-nums', report.team.waiting_now ? toneText[waitTone(report.team)] : 'text-white/25 light:text-gray-300']">{{ report.team.waiting_now }}</td>
              <td class="px-2 py-2 text-right tabular-nums text-white/80 light:text-gray-800">{{ report.team.owned_chats }}</td>
              <td class="px-3 py-2">
                <div class="flex items-end gap-px h-6 w-[132px]">
                  <span
                    v-for="(h, i) in sparkHeights(report.team.daily)"
                    :key="report.team.daily[i].date"
                    :title="sparkTitle(report.team.daily[i])"
                    :class="['flex-1 rounded-t-[2px] min-w-[2px]', h > 0 ? toneBar[tone(report.team.daily[i].median_first_reply_secs)] : 'bg-white/[0.08] light:bg-gray-200']"
                    :style="{ height: h > 0 ? `${Math.max(12, h * 100)}%` : '2px' }"
                  />
                </div>
              </td>
              <td class="pr-2"><ChevronRight class="h-4 w-4 text-white/20 group-hover:text-white/60 light:text-gray-300 light:group-hover:text-gray-600" /></td>
            </tr>
          </tbody>
        </table>
        <p class="px-3 pt-2 text-[11px] text-white/35 light:text-gray-400">
          {{ $t('staffReport.footnote', { time: formatDuration(target) }) }}
        </p>
      </div>
    </template>

    <!-- Day by day for one row -->
    <Dialog v-model:open="dialogOpen">
      <DialogContent class="max-w-3xl max-h-[88vh] overflow-y-auto">
        <template v-if="selected && report">
          <DialogHeader>
            <DialogTitle class="flex items-center gap-2">
              <template v-if="selected.kind === 'automatic'">
                <span class="h-7 w-7 rounded-full flex items-center justify-center bg-violet-500/20 light:bg-violet-100"><Bot class="h-3.5 w-3.5 text-violet-300 light:text-violet-700" /></span>
                {{ $t('staffReport.automatic') }}
              </template>
              <template v-else-if="selected.kind === 'team'">
                <Users class="h-5 w-5 text-sky-400" />{{ $t('staffReport.team') }}
              </template>
              <template v-else>
                <span :class="['h-7 w-7 rounded-full flex items-center justify-center text-[11px] font-semibold text-white', avatarHue(selected.row.user_id)]">{{ initials(selected.row.name) }}</span>
                {{ selected.row.name }}
              </template>
            </DialogTitle>
            <DialogDescription>{{ rangeLabel }} · {{ $t('staffReport.daysIn', { zone: report.timezone }) }}</DialogDescription>
          </DialogHeader>

          <!-- Automatic: where it came from -->
          <div v-if="selected.kind === 'automatic'" class="space-y-3">
            <p class="text-xs text-white/50 light:text-gray-500">{{ $t('staffReport.automaticExplain') }}</p>
            <div class="grid grid-cols-2 sm:grid-cols-3 gap-2">
              <div v-for="s in report.automatic.sources" :key="s.kind + (s.label || '')" class="rounded-lg border border-violet-500/20 bg-violet-500/[0.06] px-3 py-2 light:bg-violet-50 light:border-violet-100">
                <div class="text-[11px] font-medium text-violet-300 light:text-violet-700 truncate">{{ sourceName(s) }}</div>
                <div class="text-lg font-bold text-white light:text-gray-900">{{ s.messages_sent }}</div>
                <div class="text-[11px] text-white/40 light:text-gray-500">{{ $t('staffReport.chatsReachedCount', { count: s.chats_reached }) }}</div>
              </div>
              <div v-if="report.automatic.sources.length === 0" class="col-span-full text-sm text-white/40 light:text-gray-500">{{ $t('staffReport.noAutomatic') }}</div>
            </div>
            <table class="w-full text-sm">
              <thead>
                <tr class="text-[11px] uppercase tracking-wide text-white/40 light:text-gray-500">
                  <th class="text-left font-medium py-1.5">{{ $t('staffReport.day') }}</th>
                  <th class="text-right font-medium py-1.5">{{ $t('staffReport.messagesSent') }}</th>
                  <th class="text-right font-medium py-1.5">{{ $t('staffReport.chatsReached') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="d in selectedDays" :key="d.date" :class="d.messages_sent ? '' : 'opacity-40'">
                  <td class="py-1 text-white/80 light:text-gray-700">{{ dayLabel(d.date, locale, true) }}</td>
                  <td class="py-1 text-right tabular-nums text-violet-200 light:text-violet-800 font-semibold">{{ d.messages_sent }}</td>
                  <td class="py-1 text-right tabular-nums text-white/70 light:text-gray-700">{{ d.chats_answered }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <!-- A person or the team -->
          <div v-else class="space-y-3">
            <div class="flex flex-wrap gap-2 text-xs">
              <span class="rounded-md px-2 py-1 bg-sky-500/10 text-sky-300 light:bg-sky-50 light:text-sky-700">{{ $t('staffReport.chatsAnswered') }} <b class="tabular-nums">{{ selected.row.chats_answered }}</b></span>
              <span class="rounded-md px-2 py-1 bg-indigo-500/10 text-indigo-300 light:bg-indigo-50 light:text-indigo-700">{{ $t('staffReport.messagesSent') }} <b class="tabular-nums">{{ selected.row.messages_sent }}</b></span>
              <span :class="['rounded-md px-2 py-1 ring-1', toneChip[tone(selected.row.median_first_reply_secs)]]">{{ $t('staffReport.median') }} <b>{{ formatDuration(selected.row.median_first_reply_secs) }}</b></span>
              <span :class="['rounded-md px-2 py-1 ring-1', toneChip[tone(selected.row.p90_first_reply_secs)]]">{{ $t('staffReport.p90') }} <b>{{ formatDuration(selected.row.p90_first_reply_secs) }}</b></span>
              <span :class="['rounded-md px-2 py-1 ring-1', toneChip[shareTone(share(selected.row))]]">{{ $t('staffReport.onTarget', { time: formatDuration(target) }) }} <b>{{ share(selected.row) == null ? '—' : `${share(selected.row)}%` }}</b></span>
              <span :class="['rounded-md px-2 py-1 ring-1', selected.row.waiting_now ? toneChip[waitTone(selected.row)] : toneChip.good]">
                {{ $t('staffReport.waitingNow') }} <b class="tabular-nums">{{ selected.row.waiting_now }}</b>
                <template v-if="selected.row.waiting_now"> · {{ waitAge(selected.row) }}</template>
              </span>
              <span class="rounded-md px-2 py-1 bg-white/[0.05] text-white/70 light:bg-gray-100 light:text-gray-700">{{ $t('staffReport.owns') }} <b class="tabular-nums">{{ selected.row.owned_chats }}</b></span>
            </div>
            <table class="w-full text-sm border-separate border-spacing-0">
              <thead>
                <tr class="text-[11px] uppercase tracking-wide text-white/40 light:text-gray-500">
                  <th class="text-left font-medium py-1.5 border-b border-white/[0.06] light:border-gray-100">{{ $t('staffReport.day') }}</th>
                  <th class="text-left font-medium py-1.5 pl-2 border-b border-white/[0.06] light:border-gray-100">{{ $t('staffReport.chatsAnswered') }}</th>
                  <th class="text-right font-medium py-1.5 border-b border-white/[0.06] light:border-gray-100">{{ $t('staffReport.messagesSent') }}</th>
                  <th class="text-right font-medium py-1.5 border-b border-white/[0.06] light:border-gray-100">{{ $t('staffReport.firstReplies') }}</th>
                  <th class="text-right font-medium py-1.5 border-b border-white/[0.06] light:border-gray-100">{{ $t('staffReport.median') }}</th>
                  <th class="text-right font-medium py-1.5 border-b border-white/[0.06] light:border-gray-100">{{ $t('staffReport.p90') }}</th>
                  <th class="text-right font-medium py-1.5 border-b border-white/[0.06] light:border-gray-100">{{ $t('staffReport.onTarget', { time: formatDuration(target) }) }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="d in selectedDays" :key="d.date" :class="d.messages_sent ? '' : 'opacity-40'">
                  <td class="py-1 pr-2 whitespace-nowrap text-white/80 light:text-gray-700 border-b border-white/[0.03] light:border-gray-50">{{ dayLabel(d.date, locale, true) }}</td>
                  <td class="py-1 pl-2 border-b border-white/[0.03] light:border-gray-50">
                    <div class="flex items-center gap-2">
                      <div class="h-2 w-28 rounded-full bg-white/[0.05] light:bg-gray-100 overflow-hidden">
                        <div :class="['h-full rounded-full', toneBar[tone(d.median_first_reply_secs)]]" :style="{ width: `${(d.chats_answered / busiestDay) * 100}%` }" />
                      </div>
                      <span class="tabular-nums font-semibold text-white light:text-gray-900">{{ d.chats_answered }}</span>
                    </div>
                  </td>
                  <td class="py-1 text-right tabular-nums text-white/70 light:text-gray-700 border-b border-white/[0.03] light:border-gray-50">{{ d.messages_sent }}</td>
                  <td class="py-1 text-right tabular-nums text-white/70 light:text-gray-700 border-b border-white/[0.03] light:border-gray-50">{{ d.first_replies }}</td>
                  <td :class="['py-1 text-right tabular-nums font-semibold border-b border-white/[0.03] light:border-gray-50', toneText[tone(d.median_first_reply_secs)]]">{{ formatDuration(d.median_first_reply_secs) }}</td>
                  <td :class="['py-1 text-right tabular-nums border-b border-white/[0.03] light:border-gray-50', toneText[tone(d.p90_first_reply_secs)]]">{{ formatDuration(d.p90_first_reply_secs) }}</td>
                  <td :class="['py-1 text-right tabular-nums border-b border-white/[0.03] light:border-gray-50', toneText[shareTone(share(d))]]">{{ share(d) == null ? '—' : `${share(d)}%` }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </DialogContent>
    </Dialog>
  </section>
</template>
