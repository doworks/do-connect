<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { CheckCircle2, AlertTriangle, Link2, Loader2, Search, Stethoscope, Undo2 } from 'lucide-vue-next'
import { clinicLinksService, type ClinicLink, type ClinicStaff } from '@/services/api'
import { getErrorMessage } from '@/lib/api-utils'

// Settings, a user: which clinic system staff login they act as in the
// patient panel. Hidden when the panel isn't set up.
const props = defineProps<{ userId: string; userEmail: string; canWrite: boolean }>()
const emit = defineEmits<{ changed: [link: ClinicLink] }>()

const { t } = useI18n()
const available = ref(false)
const loading = ref(true)
const problem = ref('')
const link = ref<ClinicLink | null>(null)
const choosing = ref(false)
const search = ref('')
const searching = ref(false)
const results = ref<ClinicStaff[]>([])
const saving = ref('')

async function load() {
  loading.value = true
  problem.value = ''
  try {
    const response = await clinicLinksService.links()
    const links: ClinicLink[] = (response.data.data || response.data).links || []
    link.value = links.find(l => l.user_id === props.userId) || null
    available.value = true
  } catch (error: any) {
    available.value = error?.response?.status !== 404
    problem.value = getErrorMessage(error, t('clinicLink.loadFailed'))
  } finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch(search, () => {
  clearTimeout(timer)
  timer = setTimeout(find, 250)
})

async function find() {
  searching.value = true
  try {
    const response = await clinicLinksService.searchStaff(search.value.trim())
    results.value = (response.data.data || response.data).users || []
  } catch (error: any) {
    results.value = []
    toast.error(getErrorMessage(error, t('clinicLink.searchFailed')))
  } finally {
    searching.value = false
  }
}

function startChoosing() {
  choosing.value = true
  search.value = ''
  find()
}

async function save(clinicUser: string) {
  saving.value = clinicUser || 'own'
  try {
    const response = await clinicLinksService.link(props.userId, clinicUser)
    link.value = response.data.data || response.data
    choosing.value = false
    toast.success(clinicUser ? t('clinicLink.linked', { name: link.value?.staff_name || clinicUser }) : t('clinicLink.unlinked'))
    emit('changed', link.value!)
  } catch (error: any) {
    toast.error(getErrorMessage(error, t('clinicLink.saveFailed')))
  } finally {
    saving.value = ''
  }
}

onMounted(load)
watch(() => props.userId, load)
</script>

<template>
  <Card v-if="available" data-test="clinic-link">
    <CardHeader class="pb-3">
      <CardTitle class="text-sm font-medium flex items-center gap-2">
        <Stethoscope class="h-4 w-4 text-sky-400 light:text-sky-700" />{{ t('clinicLink.title') }}
      </CardTitle>
      <CardDescription>{{ t('clinicLink.desc') }}</CardDescription>
    </CardHeader>
    <CardContent class="space-y-3">
      <Loader2 v-if="loading" class="h-4 w-4 animate-spin text-muted-foreground" />
      <p v-else-if="problem" class="text-sm text-destructive">{{ problem }}</p>
      <template v-else-if="link">
        <div
          :class="['flex items-start gap-3 rounded-lg px-3 py-2.5',
                   link.linked ? 'bg-emerald-500/10 text-emerald-200 light:bg-emerald-50 light:text-emerald-800'
                               : 'bg-amber-500/10 text-amber-200 light:bg-amber-50 light:text-amber-800']"
          data-test="link-status"
        >
          <component :is="link.linked ? CheckCircle2 : AlertTriangle" class="h-4 w-4 mt-0.5 shrink-0" />
          <div class="text-sm min-w-0">
            <p v-if="link.linked" class="font-medium">{{ t('clinicLink.actsAs', { name: link.staff_name }) }}</p>
            <p v-else class="font-medium">{{ t('clinicLink.notLinked') }}</p>
            <p class="opacity-80 break-all">{{ link.linked ? link.acts_as : t('clinicLink.notLinkedHint', { email: link.acts_as }) }}</p>
          </div>
        </div>

        <div v-if="canWrite && !choosing" class="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" data-test="choose" @click="startChoosing">
            <Link2 class="h-4 w-4 mr-1" />{{ link.clinic_user ? t('clinicLink.change') : t('clinicLink.choose') }}
          </Button>
          <Button v-if="link.clinic_user" size="sm" variant="ghost" data-test="use-own" :disabled="!!saving" @click="save('')">
            <Loader2 v-if="saving === 'own'" class="h-4 w-4 mr-1 animate-spin" /><Undo2 v-else class="h-4 w-4 mr-1" />
            {{ t('clinicLink.useOwn', { email: userEmail }) }}
          </Button>
        </div>

        <div v-if="choosing" class="space-y-2">
          <div class="relative">
            <Search class="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
            <Input v-model="search" class="pl-8" :placeholder="t('clinicLink.searchPlaceholder')" data-test="staff-search" autofocus />
          </div>
          <div class="rounded-lg border border-white/[0.08] light:border-gray-200 divide-y divide-white/[0.06] light:divide-gray-100 max-h-72 overflow-y-auto">
            <p v-if="searching && !results.length" class="p-3 text-sm text-muted-foreground"><Loader2 class="h-4 w-4 animate-spin inline mr-1" />{{ t('clinicLink.searching') }}</p>
            <p v-else-if="!results.length" class="p-3 text-sm text-muted-foreground">{{ t('clinicLink.noStaff') }}</p>
            <button
              v-for="staff in results"
              :key="staff.email"
              type="button"
              data-test="staff-option"
              class="w-full text-left p-3 hover:bg-white/[0.04] light:hover:bg-gray-50 flex items-center gap-3 disabled:opacity-60"
              :disabled="!!saving"
              @click="save(staff.email)"
            >
              <div class="min-w-0 flex-1">
                <p class="text-sm font-medium truncate">{{ staff.full_name }}</p>
                <p class="text-xs text-muted-foreground truncate">{{ staff.email }}</p>
                <div v-if="staff.roles.length" class="flex flex-wrap gap-1 mt-1">
                  <Badge v-for="role in staff.roles" :key="role" variant="secondary" class="text-[10px] font-normal">{{ role }}</Badge>
                  <span v-if="staff.more_roles" class="text-[10px] text-muted-foreground">+{{ staff.more_roles }}</span>
                </div>
              </div>
              <Loader2 v-if="saving === staff.email" class="h-4 w-4 animate-spin shrink-0" />
              <Badge v-else-if="staff.email === link.acts_as" variant="outline" class="shrink-0">{{ t('clinicLink.current') }}</Badge>
            </button>
          </div>
          <p class="text-xs text-muted-foreground">{{ t('clinicLink.warning') }}</p>
          <Button size="sm" variant="ghost" data-test="cancel-choose" @click="choosing = false">{{ t('common.cancel') }}</Button>
        </div>
      </template>
    </CardContent>
  </Card>
</template>
