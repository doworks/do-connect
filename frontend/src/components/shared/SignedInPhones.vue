<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import ConfirmDialog from '@/components/shared/ConfirmDialog.vue'
import { Smartphone, Loader2 } from 'lucide-vue-next'
import { mobileDevicesService, type MobileDevice } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'
import { formatDateTime } from '@/lib/utils'

// Phones signed in through the mobile app. Without userId it lists the
// viewer's own phones; with userId (admins) that user's phones.
const props = withDefaults(defineProps<{
  userId?: string
  canRemove?: boolean
}>(), {
  canRemove: true,
})

const { t } = useI18n()
const devices = ref<MobileDevice[]>([])
const isLoading = ref(true)
const removing = ref<MobileDevice | null>(null)
const confirmOpen = ref(false)
const isRemoving = ref(false)

async function load() {
  isLoading.value = true
  try {
    const params = props.userId ? { user_id: props.userId } : { scope: 'mine' as const }
    const response = await mobileDevicesService.list(params)
    const data = response.data.data || response.data
    devices.value = data.devices || []
  } catch {
    devices.value = []
  } finally {
    isLoading.value = false
  }
}

function askRemove(device: MobileDevice) {
  removing.value = device
  confirmOpen.value = true
}

async function remove() {
  if (!removing.value) return
  isRemoving.value = true
  try {
    await mobileDevicesService.remove(removing.value.id)
    devices.value = devices.value.filter(d => d.id !== removing.value?.id)
    toast.success(t('phones.removed'))
    confirmOpen.value = false
  } catch (error: any) {
    toast.error(getErrorMessage(error, t('phones.removeFailed')))
  } finally {
    isRemoving.value = false
  }
}

watch(() => props.userId, load, { immediate: true })
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle class="flex items-center gap-2">
        <Smartphone class="h-4 w-4" />
        {{ $t('phones.title') }}
      </CardTitle>
      <CardDescription>{{ $t('phones.description') }}</CardDescription>
    </CardHeader>
    <CardContent>
      <div v-if="isLoading" class="flex justify-center py-4">
        <Loader2 class="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
      <p v-else-if="devices.length === 0" class="text-sm text-muted-foreground">
        {{ $t('phones.none') }}
      </p>
      <ul v-else class="divide-y">
        <li v-for="device in devices" :key="device.id" class="flex items-center justify-between gap-3 py-3">
          <div class="min-w-0">
            <p class="font-medium truncate">
              {{ device.name }}
              <span v-if="device.model" class="text-muted-foreground font-normal">· {{ device.model }}</span>
            </p>
            <p class="text-xs text-muted-foreground">
              {{ $t('phones.lastSeen', { time: formatDateTime(device.last_seen_at) }) }}
              · {{ $t('phones.signedIn', { time: formatDateTime(device.created_at) }) }}
            </p>
          </div>
          <div class="flex items-center gap-2 shrink-0">
            <Badge v-if="device.push_enabled" variant="outline">{{ $t('phones.notifications') }}</Badge>
            <Button v-if="canRemove" variant="outline" size="sm" @click="askRemove(device)">
              {{ $t('common.remove', 'Remove') }}
            </Button>
          </div>
        </li>
      </ul>
    </CardContent>
  </Card>

  <ConfirmDialog
    v-model:open="confirmOpen"
    :title="$t('phones.removeTitle')"
    :description="$t('phones.removeDescription', { name: removing?.name || '' })"
    :confirm-label="$t('common.remove', 'Remove')"
    :cancel-label="$t('common.cancel')"
    variant="destructive"
    :is-submitting="isRemoving"
    @confirm="remove"
  />
</template>
