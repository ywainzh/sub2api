<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6">
      <header class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div class="flex items-center gap-3">
            <div class="flex h-11 w-11 items-center justify-center rounded-2xl bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
              <Icon name="terminal" size="lg" aria-hidden="true" />
            </div>
            <div>
              <h1 class="text-2xl font-bold text-gray-900 dark:text-white">
                {{ t('admin.accounts.statusCheck.title') }}
              </h1>
              <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-gray-300">
                {{ t('admin.accounts.statusCheck.description') }}
              </p>
            </div>
          </div>
        </div>
        <span
          class="inline-flex w-fit items-center gap-2 rounded-full border px-3 py-1.5 text-xs font-semibold"
          :class="runStateClass"
          role="status"
        >
          <span class="h-2 w-2 rounded-full" :class="runStateDotClass"></span>
          {{ runStateLabel }}
        </span>
      </header>

      <section class="card" :aria-busy="running">
        <div class="card-header">
          <div class="flex items-center gap-2">
            <Icon name="beaker" size="md" class="text-primary-500" />
            <h2 class="font-semibold text-gray-900 dark:text-white">
              {{ t('admin.accounts.statusCheck.configuration') }}
            </h2>
          </div>
        </div>
        <div class="card-body space-y-5">
          <div
            v-if="loadError"
            class="flex items-start gap-3 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800/60 dark:bg-red-950/30 dark:text-red-200"
            role="alert"
          >
            <Icon name="exclamationCircle" size="md" class="mt-0.5 flex-shrink-0" />
            <span>{{ loadError }}</span>
          </div>

          <div
            v-if="!groupsLoading && groupOptions.length === 0"
            class="flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800/60 dark:bg-amber-950/30 dark:text-amber-200"
            role="status"
          >
            <Icon name="exclamationTriangle" size="md" class="mt-0.5 flex-shrink-0" />
            <div>
              <p class="font-medium">{{ t('admin.accounts.statusCheck.noGroups') }}</p>
              <p class="mt-1 text-xs leading-5 opacity-90">
                {{ t('admin.accounts.statusCheck.noGroupsHint') }}
              </p>
            </div>
          </div>

          <div class="grid gap-4 lg:grid-cols-3">
            <div>
              <label for="status-check-group" class="input-label">
                {{ t('admin.accounts.statusCheck.groupLabel') }}
              </label>
              <Select
                id="status-check-group"
                v-model="selectedGroupId"
                :options="groupOptions"
                :placeholder="groupsLoading ? t('common.loading') : t('admin.accounts.statusCheck.groupPlaceholder')"
                :disabled="running || groupsLoading"
                :searchable="true"
                :empty-text="t('admin.accounts.statusCheck.noGroups')"
                :aria-label="t('admin.accounts.statusCheck.groupLabel')"
              />
              <p class="input-hint">{{ t('admin.accounts.statusCheck.groupHint') }}</p>
            </div>

            <div>
              <label for="status-check-model" class="input-label">
                {{ t('admin.accounts.statusCheck.modelLabel') }}
              </label>
              <Select
                id="status-check-model"
                v-model="selectedModelId"
                :options="modelOptions"
                :placeholder="modelsLoading ? t('common.loading') : t('admin.accounts.statusCheck.modelPlaceholder')"
                :disabled="running || modelsLoading || selectedGroupId === null"
                :searchable="true"
                :creatable="true"
                :creatable-prefix="t('admin.accounts.statusCheck.useCustomModel')"
                :aria-label="t('admin.accounts.statusCheck.modelLabel')"
              />
              <p class="input-hint">{{ t('admin.accounts.statusCheck.modelHint') }}</p>
            </div>

            <div>
              <label for="status-check-mode" class="input-label">
                {{ t('admin.accounts.statusCheck.modeLabel') }}
              </label>
              <Select
                id="status-check-mode"
                v-model="selectedMode"
                :options="modeOptions"
                :disabled="running"
                :searchable="false"
                :aria-label="t('admin.accounts.statusCheck.modeLabel')"
              />
              <p class="input-hint">{{ t('admin.accounts.statusCheck.modeHint') }}</p>
            </div>
          </div>

          <div class="flex flex-col gap-3 border-t border-gray-100 pt-5 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
            <div class="text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.statusCheck.workerHint', { count: 5 }) }}
            </div>
            <div class="flex flex-wrap gap-3">
              <button
                v-if="running"
                type="button"
                class="btn btn-danger min-h-11 cursor-pointer"
                @click="stopCheck"
              >
                <Icon name="xCircle" size="sm" />
                {{ t('admin.accounts.statusCheck.stop') }}
              </button>
              <button
                v-else
                type="button"
                class="btn btn-primary min-h-11 cursor-pointer"
                :disabled="!canStart"
                @click="startCheck"
              >
                <Icon name="play" size="sm" />
                {{ t('admin.accounts.statusCheck.start') }}
              </button>
            </div>
          </div>
        </div>
      </section>

      <section aria-labelledby="status-check-summary-title">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h2 id="status-check-summary-title" class="font-semibold text-gray-900 dark:text-white">
            {{ t('admin.accounts.statusCheck.summary') }}
          </h2>
          <span class="text-sm tabular-nums text-gray-500 dark:text-gray-400">
            {{ progressPercent }}%
          </span>
        </div>
        <div class="mb-4 h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700" aria-hidden="true">
          <div
            class="h-full rounded-full bg-gradient-to-r from-primary-500 to-emerald-500 transition-[width] duration-300 motion-reduce:transition-none"
            :style="{ width: `${progressPercent}%` }"
          ></div>
        </div>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
          <StatCard
            :title="t('admin.accounts.statusCheck.totalProgress')"
            :value="`${stats.completed}/${stats.total}`"
            :icon="ProgressIcon"
            icon-variant="primary"
          />
          <StatCard
            :title="t('admin.accounts.statusCheck.normal')"
            :value="stats.normal"
            :icon="NormalIcon"
            icon-variant="success"
          />
          <StatCard
            :title="t('admin.accounts.statusCheck.unauthorized')"
            :value="stats.unauthorized"
            :icon="UnauthorizedIcon"
            icon-variant="danger"
          />
          <StatCard
            :title="t('admin.accounts.statusCheck.quotaExhausted')"
            :value="stats.quota_exhausted"
            :icon="QuotaIcon"
            icon-variant="warning"
          />
          <StatCard
            :title="t('admin.accounts.statusCheck.forbidden')"
            :value="stats.forbidden"
            :icon="ForbiddenIcon"
            icon-variant="danger"
          />
          <StatCard
            :title="t('admin.accounts.statusCheck.otherError')"
            :value="stats.other_error"
            :icon="OtherErrorIcon"
            icon-variant="danger"
          />
        </div>
      </section>

      <section class="overflow-hidden rounded-2xl border border-slate-800 bg-slate-950 shadow-xl" aria-labelledby="status-check-terminal-title">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-800 bg-slate-900/90 px-4 py-3 sm:px-5">
          <div class="flex items-center gap-2">
            <span class="h-3 w-3 rounded-full bg-red-400" aria-hidden="true"></span>
            <span class="h-3 w-3 rounded-full bg-amber-400" aria-hidden="true"></span>
            <span class="h-3 w-3 rounded-full bg-emerald-400" aria-hidden="true"></span>
            <h2 id="status-check-terminal-title" class="ml-2 text-sm font-semibold text-slate-100">
              {{ t('admin.accounts.statusCheck.terminalTitle') }}
            </h2>
          </div>
          <button
            type="button"
            class="inline-flex min-h-11 cursor-pointer items-center gap-2 rounded-lg px-3 py-2 text-sm text-slate-300 transition-colors duration-200 hover:bg-slate-800 hover:text-white focus:outline-none focus:ring-2 focus:ring-primary-400"
            :disabled="terminalLines.length === 0"
            @click="copyTerminal"
          >
            <Icon name="copy" size="sm" />
            {{ t('admin.accounts.statusCheck.copyLogs') }}
          </button>
        </div>
        <div
          ref="terminalRef"
          class="h-[420px] overflow-y-auto overscroll-contain p-4 font-mono text-sm leading-6 sm:p-5"
          role="log"
          aria-live="polite"
          :aria-label="t('admin.accounts.statusCheck.terminalTitle')"
        >
          <p v-if="terminalLines.length === 0" class="text-slate-500">
            {{ t('admin.accounts.statusCheck.terminalEmpty') }}
          </p>
          <p
            v-for="line in terminalLines"
            :key="line.id"
            class="whitespace-pre-wrap break-words"
            :class="terminalToneClass(line.tone)"
          >
            {{ line.text }}
          </p>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import StatCard from '@/components/common/StatCard.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import type {
  AccountStatusCheckCategory,
  AccountStatusCheckEvent,
  AccountStatusCheckMode,
  AccountStatusCheckStats
} from '@/api/admin/accounts'
import type { Account, AdminGroup } from '@/types'
import { useClipboard } from '@/composables/useClipboard'

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const DEFAULT_MODEL = 'gpt-5.5'

type RunState = 'idle' | 'running' | 'completed' | 'stopped' | 'error'
type TerminalTone = 'muted' | 'info' | 'success' | 'warning' | 'danger'
type StatusIconName = 'chart' | 'checkCircle' | 'key' | 'clock' | 'shield' | 'exclamationTriangle'

interface TerminalLine {
  id: number
  text: string
  tone: TerminalTone
}

const makeStatIcon = (name: StatusIconName) =>
  defineComponent({
    name: `StatusCheck${name}Icon`,
    setup: () => () => h(Icon, { name, size: 'lg' })
  })

const ProgressIcon = makeStatIcon('chart')
const NormalIcon = makeStatIcon('checkCircle')
const UnauthorizedIcon = makeStatIcon('key')
const QuotaIcon = makeStatIcon('clock')
const ForbiddenIcon = makeStatIcon('shield')
const OtherErrorIcon = makeStatIcon('exclamationTriangle')

const emptyStats = (): AccountStatusCheckStats => ({
  total: 0,
  completed: 0,
  normal: 0,
  unauthorized: 0,
  quota_exhausted: 0,
  forbidden: 0,
  other_error: 0
})

const groups = ref<AdminGroup[]>([])
const selectedGroupId = ref<number | null>(null)
const selectedModelId = ref(DEFAULT_MODEL)
const selectedMode = ref<AccountStatusCheckMode>('default')
const modelOptions = ref<SelectOption[]>([{ value: DEFAULT_MODEL, label: DEFAULT_MODEL }])
const groupsLoading = ref(false)
const modelsLoading = ref(false)
const loadError = ref('')
const runState = ref<RunState>('idle')
const stats = ref<AccountStatusCheckStats>(emptyStats())
const terminalLines = ref<TerminalLine[]>([])
const terminalRef = ref<HTMLElement | null>(null)
let abortController: AbortController | null = null
let terminalLineID = 0
let modelLoadSequence = 0

const running = computed(() => runState.value === 'running')
const canStart = computed(
  () => selectedGroupId.value !== null && selectedModelId.value.trim() !== '' && !groupsLoading.value && !modelsLoading.value
)
const progressPercent = computed(() => {
  if (stats.value.total <= 0) return 0
  return Math.min(100, Math.round((stats.value.completed / stats.value.total) * 100))
})

const groupOptions = computed<SelectOption[]>(() =>
  groups.value.map((group) => ({
    value: group.id,
    label:
      group.status === 'active'
        ? group.name
        : `${group.name} · ${t('admin.accounts.statusCheck.inactiveGroup')}`,
    status: group.status
  }))
)

const modeOptions = computed<SelectOption[]>(() => [
  { value: 'default', label: t('admin.accounts.openai.testModeDefault') },
  { value: 'compact', label: t('admin.accounts.openai.testModeCompact') }
])

const runStateLabel = computed(() => t(`admin.accounts.statusCheck.runState.${runState.value}`))
const runStateClass = computed(() => {
  const classes: Record<RunState, string> = {
    idle: 'border-gray-200 bg-white text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300',
    running: 'border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-800/60 dark:bg-blue-950/30 dark:text-blue-200',
    completed: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800/60 dark:bg-emerald-950/30 dark:text-emerald-200',
    stopped: 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800/60 dark:bg-amber-950/30 dark:text-amber-200',
    error: 'border-red-200 bg-red-50 text-red-700 dark:border-red-800/60 dark:bg-red-950/30 dark:text-red-200'
  }
  return classes[runState.value]
})
const runStateDotClass = computed(() => {
  const classes: Record<RunState, string> = {
    idle: 'bg-gray-400',
    running: 'animate-pulse bg-blue-500 motion-reduce:animate-none',
    completed: 'bg-emerald-500',
    stopped: 'bg-amber-500',
    error: 'bg-red-500'
  }
  return classes[runState.value]
})

function terminalToneClass(tone: TerminalTone): string {
  return {
    muted: 'text-slate-500',
    info: 'text-sky-300',
    success: 'text-emerald-300',
    warning: 'text-amber-300',
    danger: 'text-red-300'
  }[tone]
}

async function scrollTerminalToBottom() {
  await nextTick()
  if (terminalRef.value) terminalRef.value.scrollTop = terminalRef.value.scrollHeight
}

function appendTerminal(text: string, tone: TerminalTone = 'muted') {
  const normalized = text.trim()
  if (!normalized) return
  terminalLines.value.push({ id: ++terminalLineID, text: normalized, tone })
  void scrollTerminalToBottom()
}

function accountPrefix(event: AccountStatusCheckEvent): string {
  const name = event.account_name || t('admin.accounts.statusCheck.unknownAccount')
  const id = event.account_id ? `#${event.account_id}` : '#?'
  return `[${name} ${id}]`
}

function categoryLabel(category?: AccountStatusCheckCategory): string {
  if (!category) return t('admin.accounts.statusCheck.otherError')
  return t(`admin.accounts.statusCheck.category.${category}`)
}

function categoryTone(category?: AccountStatusCheckCategory): TerminalTone {
  switch (category) {
    case 'normal':
      return 'success'
    case 'quota_exhausted':
      return 'warning'
    case 'unauthorized':
    case 'forbidden':
    case 'other_error':
      return 'danger'
    default:
      return 'muted'
  }
}

function mergeStats(next?: AccountStatusCheckStats) {
  if (next) stats.value = { ...next }
}

function handleStatusEvent(event: AccountStatusCheckEvent) {
  mergeStats(event.stats)
  switch (event.type) {
    case 'batch_start':
      appendTerminal(
        t('admin.accounts.statusCheck.log.batchStart', {
          group: event.group_name || selectedGroupName.value,
          total: event.total ?? event.stats?.total ?? 0,
          model: event.model_id || selectedModelId.value,
          mode: event.mode || selectedMode.value
        }),
        'info'
      )
      break
    case 'account_start':
      appendTerminal(
        `${accountPrefix(event)} ${t('admin.accounts.statusCheck.log.accountStart', { priority: event.account_priority ?? 0 })}`,
        'info'
      )
      break
    case 'account_log':
      appendTerminal(`${accountPrefix(event)} ${event.text || ''}`, event.log_type === 'error' ? 'danger' : 'muted')
      break
    case 'account_result': {
      const status = event.http_status ? `HTTP ${event.http_status}` : categoryLabel(event.category)
      const latency = event.latency_ms !== undefined ? ` · ${event.latency_ms}ms` : ''
      const detail = event.error ? ` · ${event.error}` : ''
      appendTerminal(
        `${accountPrefix(event)} ${categoryLabel(event.category)} (${status})${latency}${detail}`,
        categoryTone(event.category)
      )
      break
    }
    case 'batch_progress':
      break
    case 'batch_complete':
      runState.value = event.canceled ? 'stopped' : 'completed'
      appendTerminal(
        event.canceled
          ? t('admin.accounts.statusCheck.log.batchStopped')
          : t('admin.accounts.statusCheck.log.batchComplete', {
              completed: event.completed ?? event.stats?.completed ?? 0,
              total: event.total ?? event.stats?.total ?? 0
            }),
        event.canceled ? 'warning' : 'success'
      )
      break
  }
}

const selectedGroupName = computed(
  () => groups.value.find((group) => group.id === selectedGroupId.value)?.name || '-'
)

function collectMappedModels(accounts: Account[]): string[] {
  const models = new Set<string>()
  for (const account of accounts) {
    const credentials = account.credentials as Record<string, unknown> | undefined
    for (const key of ['model_mapping', 'compact_model_mapping']) {
      const mapping = credentials?.[key]
      if (!mapping || typeof mapping !== 'object' || Array.isArray(mapping)) continue
      for (const [source, target] of Object.entries(mapping as Record<string, unknown>)) {
        if (source.trim() && !source.includes('*')) models.add(source.trim())
        if (typeof target === 'string' && target.trim() && !target.includes('*')) models.add(target.trim())
      }
    }
  }
  return [...models]
}

async function loadGroups() {
  groupsLoading.value = true
  loadError.value = ''
  try {
    const allGroups = await adminAPI.groups.getAllIncludingInactive()
    groups.value = allGroups.filter((group) => group.platform === 'openai')
    if (groups.value.length > 0 && selectedGroupId.value === null) {
      selectedGroupId.value = groups.value[0].id
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : t('admin.accounts.statusCheck.loadGroupsFailed')
    loadError.value = message
  } finally {
    groupsLoading.value = false
  }
}

async function loadModels(groupID: number) {
  const sequence = ++modelLoadSequence
  modelsLoading.value = true
  loadError.value = ''
  selectedModelId.value = DEFAULT_MODEL
  try {
    const [candidates, accountPage] = await Promise.all([
      adminAPI.groups.getModelsListCandidates(groupID, 'openai'),
      adminAPI.accounts.list(1, 1000, { group: String(groupID), lite: 'false' })
    ])
    if (sequence !== modelLoadSequence) return
    const models = new Set<string>([DEFAULT_MODEL, ...candidates, ...collectMappedModels(accountPage.items)])
    const sorted = [...models]
      .map((model) => model.trim())
      .filter(Boolean)
      .sort((left, right) => {
        if (left === DEFAULT_MODEL) return -1
        if (right === DEFAULT_MODEL) return 1
        return left.localeCompare(right)
      })
    modelOptions.value = sorted.map((model) => ({ value: model, label: model }))
  } catch (error) {
    if (sequence !== modelLoadSequence) return
    modelOptions.value = [{ value: DEFAULT_MODEL, label: DEFAULT_MODEL }]
    loadError.value = error instanceof Error ? error.message : t('admin.accounts.statusCheck.loadModelsFailed')
  } finally {
    if (sequence === modelLoadSequence) modelsLoading.value = false
  }
}

watch(selectedGroupId, (groupID) => {
  if (groupID === null) {
    modelOptions.value = [{ value: DEFAULT_MODEL, label: DEFAULT_MODEL }]
    selectedModelId.value = DEFAULT_MODEL
    return
  }
  void loadModels(Number(groupID))
})

async function startCheck() {
  if (!canStart.value || selectedGroupId.value === null) return
  abortController?.abort()
  const controller = new AbortController()
  abortController = controller
  stats.value = emptyStats()
  terminalLines.value = []
  runState.value = 'running'
  appendTerminal(t('admin.accounts.statusCheck.log.connecting'), 'info')

  try {
    await adminAPI.accounts.runStatusCheck(
      {
        group_id: selectedGroupId.value,
        model_id: selectedModelId.value.trim(),
        mode: selectedMode.value
      },
      handleStatusEvent,
      controller.signal
    )
    if (runState.value === 'running') runState.value = 'completed'
  } catch (error) {
    if (controller.signal.aborted) return
    runState.value = 'error'
    const message = error instanceof Error ? error.message : t('admin.accounts.statusCheck.requestFailed')
    appendTerminal(t('admin.accounts.statusCheck.log.requestError', { error: message }), 'danger')
  } finally {
    if (abortController === controller) abortController = null
  }
}

function stopCheck() {
  if (!abortController) return
  abortController.abort()
  abortController = null
  if (runState.value === 'running') {
    runState.value = 'stopped'
    appendTerminal(t('admin.accounts.statusCheck.log.stopRequested'), 'warning')
  }
}

function cancelOnLeave() {
  abortController?.abort()
  abortController = null
}

async function copyTerminal() {
  await copyToClipboard(
    terminalLines.value.map((line) => line.text).join('\n'),
    t('admin.accounts.statusCheck.logsCopied')
  )
}

onMounted(() => {
  void loadGroups()
})
onBeforeUnmount(cancelOnLeave)
onBeforeRouteLeave(() => {
  cancelOnLeave()
  return true
})
</script>
