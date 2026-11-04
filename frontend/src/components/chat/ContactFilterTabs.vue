<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useContactsStore } from '@/stores/contacts'

// Worklist filters above the chat list, with how many chats each holds.
const { t } = useI18n()
const contactsStore = useContactsStore()

const filterTabs = computed(() => {
  const counts = contactsStore.filterCounts
  return [
    { key: 'all', label: t('chat.filterAll'), count: 0 },
    { key: 'unread', label: t('chat.filterUnread'), count: counts.unread || 0 },
    { key: 'waiting', label: t('chat.filterWaiting'), count: counts.waiting || 0 },
    { key: 'mine', label: t('chat.filterMine'), count: counts.mine || 0 },
    { key: 'unassigned', label: t('chat.filterUnassigned'), count: counts.unassigned || 0 },
  ]
})
</script>

<template>
  <div class="flex flex-wrap items-center gap-0.5 px-2 py-1.5 border-b border-white/[0.08] light:border-gray-200">
    <button
      v-for="tab in filterTabs"
      :key="tab.key"
      type="button"
      :class="[
        'shrink-0 inline-flex items-center gap-1 h-6 px-1.5 rounded-full text-[11px] font-medium transition-colors',
        contactsStore.listFilter === tab.key
          ? 'bg-emerald-600 text-white'
          : 'bg-white/[0.06] text-white/60 hover:bg-white/[0.1] light:bg-gray-100 light:text-gray-600 light:hover:bg-gray-200'
      ]"
      @click="contactsStore.setListFilter(tab.key)"
    >
      {{ tab.label }}
      <span
        v-if="tab.count > 0"
        :class="[
          'min-w-[14px] h-4 px-0.5 rounded-full text-[10px] leading-4 text-center',
          contactsStore.listFilter === tab.key
            ? 'bg-white/25 text-white'
            : tab.key === 'unread' ? 'bg-emerald-500/20 text-emerald-400 light:bg-emerald-100 light:text-emerald-700'
            : tab.key === 'waiting' ? 'bg-amber-500/20 text-amber-400 light:bg-amber-100 light:text-amber-700'
            : 'bg-white/[0.1] text-white/60 light:bg-gray-200 light:text-gray-600'
        ]"
      >{{ tab.count }}</span>
    </button>
  </div>
</template>
