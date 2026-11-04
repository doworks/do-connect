<script setup lang="ts">
import { TagBadge } from '@/components/ui/tag-badge'
import { useTagsStore } from '@/stores/tags'
import type { Contact } from '@/stores/contacts'
import { contactLanguage } from '@/lib/chatDisplay'

// Beside the name in the chat header: the language to reply in and the first
// few tags (the language tags show as the chip instead).
defineProps<{ contact: Contact }>()

const tagsStore = useTagsStore()
</script>

<template>
  <span
    v-if="contactLanguage(contact)"
    :class="[
      'shrink-0 h-4 px-1 rounded text-[9px] leading-4 font-bold tracking-wide',
      contactLanguage(contact) === 'AR'
        ? 'bg-violet-500/20 text-violet-300 light:bg-violet-100 light:text-violet-700'
        : 'bg-sky-500/20 text-sky-300 light:bg-sky-100 light:text-sky-700'
    ]"
    :title="$t('chat.replyLanguage')"
  >{{ contactLanguage(contact) }}</span>
  <TagBadge
    v-for="tagName in (contact.tags || []).filter(t => t !== 'Arabic' && t !== 'English').slice(0, 3)"
    :key="tagName"
    :color="tagsStore.getTagByName(tagName)?.color"
    class="text-[10px] h-4"
  >{{ tagName }}</TagBadge>
</template>
