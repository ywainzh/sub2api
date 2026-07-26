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
        <div class="px-5 py-4 sm:px-6">
          <div class="mb-4 flex items-center gap-2">
            <Icon name="beaker" size="sm" class="text-primary-500" />
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.accounts.statusCheck.configuration') }}
            </h2>
          </div>

          <div
            v-if="loadError"
            class="mb-4 flex items-start gap-3 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-800/60 dark:bg-red-950/30 dark:text-red-200"
            role="alert"
          >
            <Icon name="exclamationCircle" size="md" class="mt-0.5 flex-shrink-0" />
            <span>{{ loadError }}</span>
          </div>

          <div
            v-if="!groupsLoading && groupOptions.length === 0"
            class="mb-4 flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-800/60 dark:bg-amber-950/30 dark:text-amber-200"
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

          <div class="grid gap-3 lg:grid-cols-3 xl:grid-cols-[repeat(3,minmax(0,1fr))_auto] xl:items-end">
            <div>
              <label for="status-check-group" class="input-label">
                {{ t('admin.accounts.statusCheck.groupLabel') }}
              </label>
              <Select
                id="status-check-group"
                v-model="selectedGroupId"
                :options="groupOptions"
                :placeholder="groupsLoading ? t('common.loading') : t('admin.accounts.statusCheck.groupPlaceholder')"
                :disabled="running || clearingCategory !== null || groupsLoading"
                :searchable="true"
                :empty-text="t('admin.accounts.statusCheck.noGroups')"
                :aria-label="t('admin.accounts.statusCheck.groupLabel')"
              />
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
                :disabled="running || clearingCategory !== null || modelsLoading || selectedGroupId === null"
                :searchable="true"
                :creatable="true"
                :creatable-prefix="t('admin.accounts.statusCheck.useCustomModel')"
                :aria-label="t('admin.accounts.statusCheck.modelLabel')"
              />
            </div>

            <div>
              <div class="flex items-center">
                <label for="status-check-mode" class="input-label mb-0">
                  {{ t('admin.accounts.statusCheck.modeLabel') }}
                </label>
                <HelpTooltip
                  :content="t('admin.accounts.statusCheck.modeHint')"
                  width-class="w-72 max-w-[calc(100vw-2rem)]"
                >
                  <template #trigger>
                    <span
                      class="inline-flex h-4 w-4 cursor-help items-center justify-center rounded-full border border-gray-400 text-[10px] font-semibold leading-none text-gray-500 transition-colors hover:border-primary-500 hover:text-primary-600 dark:border-gray-500 dark:text-gray-400 dark:hover:border-primary-400 dark:hover:text-primary-300"
                      role="img"
                      :aria-label="t('admin.accounts.statusCheck.modeHint')"
                    >
                      ?
                    </span>
                  </template>
                </HelpTooltip>
              </div>
              <Select
                id="status-check-mode"
                v-model="selectedMode"
                :options="modeOptions"
                :disabled="running || clearingCategory !== null"
                :searchable="false"
                :aria-label="t('admin.accounts.statusCheck.modeLabel')"
              />
            </div>

            <div class="flex lg:col-span-3 lg:justify-end xl:col-span-1">
              <button
                v-if="running"
                type="button"
                class="btn btn-danger min-h-11 w-full cursor-pointer sm:w-auto xl:whitespace-nowrap"
                @click="stopCheck"
              >
                <Icon name="xCircle" size="sm" />
                {{ t('admin.accounts.statusCheck.stop') }}
              </button>
              <button
                v-else
                type="button"
                class="btn btn-primary min-h-11 w-full cursor-pointer sm:w-auto xl:whitespace-nowrap"
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
          >
            <template #action>
              <HelpTooltip :content="t('admin.accounts.statusCheck.clearTooltip')">
                <template #trigger>
                  <button
                    type="button"
                    data-test="clear-unauthorized"
                    class="status-clear-button"
                    :disabled="!canClearCategory('unauthorized')"
                    :aria-label="t('admin.accounts.statusCheck.clearCategory', { category: categoryLabel('unauthorized') })"
                    @click="requestCategoryClear('unauthorized')"
                  >
                    <Icon
                      :name="clearingCategory === 'unauthorized' ? 'refresh' : 'broom'"
                      size="sm"
                      :class="clearingCategory === 'unauthorized' ? 'animate-spin motion-reduce:animate-none' : ''"
                    />
                  </button>
                </template>
              </HelpTooltip>
            </template>
          </StatCard>
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
          >
            <template #action>
              <HelpTooltip :content="t('admin.accounts.statusCheck.clearTooltip')">
                <template #trigger>
                  <button
                    type="button"
                    data-test="clear-forbidden"
                    class="status-clear-button"
                    :disabled="!canClearCategory('forbidden')"
                    :aria-label="t('admin.accounts.statusCheck.clearCategory', { category: categoryLabel('forbidden') })"
                    @click="requestCategoryClear('forbidden')"
                  >
                    <Icon
                      :name="clearingCategory === 'forbidden' ? 'refresh' : 'broom'"
                      size="sm"
                      :class="clearingCategory === 'forbidden' ? 'animate-spin motion-reduce:animate-none' : ''"
                    />
                  </button>
                </template>
              </HelpTooltip>
            </template>
          </StatCard>
          <StatCard
            :title="t('admin.accounts.statusCheck.otherError')"
            :value="stats.other_error"
            :icon="OtherErrorIcon"
            icon-variant="danger"
          >
            <template #action>
              <HelpTooltip :content="t('admin.accounts.statusCheck.clearTooltip')">
                <template #trigger>
                  <button
                    type="button"
                    data-test="clear-other-error"
                    class="status-clear-button"
                    :disabled="!canClearCategory('other_error')"
                    :aria-label="t('admin.accounts.statusCheck.clearCategory', { category: categoryLabel('other_error') })"
                    @click="requestCategoryClear('other_error')"
                  >
                    <Icon
                      :name="clearingCategory === 'other_error' ? 'refresh' : 'broom'"
                      size="sm"
                      :class="clearingCategory === 'other_error' ? 'animate-spin motion-reduce:animate-none' : ''"
                    />
                  </button>
                </template>
              </HelpTooltip>
            </template>
          </StatCard>
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

      <ConfirmDialog
        :show="pendingClearCategory !== null"
        :title="clearDialogTitle"
        :message="clearDialogMessage"
        :confirm-text="clearConfirmText"
        :cancel-text="t('common.cancel')"
        :danger="true"
        @confirm="confirmCategoryClear"
        @cancel="pendingClearCategory = null"
      >
        <div v-if="pendingClearAccounts.length" class="rounded-lg border border-red-200 bg-red-50/70 p-3 dark:border-red-900/70 dark:bg-red-950/20">
          <p class="text-xs font-medium text-red-700 dark:text-red-300">
            {{ t('admin.accounts.statusCheck.clearAccountPreview') }}
          </p>
          <ul class="mt-2 max-h-36 space-y-1 overflow-y-auto text-xs text-gray-700 dark:text-gray-300">
            <li v-for="account in pendingClearAccountPreview" :key="account.accountId" class="truncate">
              {{ account.name }} <span class="text-gray-400">#{{ account.accountId }}</span>
            </li>
          </ul>
          <p v-if="pendingClearAccounts.length > pendingClearAccountPreview.length" class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.statusCheck.clearMoreAccounts', { count: pendingClearAccounts.length - pendingClearAccountPreview.length }) }}
          </p>
        </div>
      </ConfirmDialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import StatCard from '@/components/common/StatCard.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import type {
  AccountStatusCheckCategory,
  AccountStatusCheckDeleteFailure,
  AccountStatusCheckEvent,
  AccountStatusCheckMode,
  AccountStatusCheckStats
} from '@/api/admin/accounts'
import type { Account, AdminGroup } from '@/types'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const appStore = useAppStore()

const DEFAULT_MODEL = 'gpt-5.5'

type RunState = 'idle' | 'running' | 'completed' | 'stopped' | 'error'
type TerminalTone = 'muted' | 'info' | 'success' | 'warning' | 'danger'
type StatusIconName = 'chart' | 'checkCircle' | 'key' | 'clock' | 'shield' | 'exclamationTriangle'
type ClearableCategory = 'unauthorized' | 'forbidden' | 'other_error'

interface TerminalLine {
  id: number
  text: string
  tone: TerminalTone
}

interface ResponseBuffer {
  prefix: string
  text: string
}

interface StatusCheckAccountResult {
  accountId: number
  name: string
  category: AccountStatusCheckCategory
}

interface StatusCheckRunSnapshot {
  groupId: number
  groupName: string
  modelId: string
  mode: AccountStatusCheckMode
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
const responseBuffers = new Map<number, ResponseBuffer>()
const accountResults = ref<Record<number, StatusCheckAccountResult>>({})
const runSnapshot = ref<StatusCheckRunSnapshot | null>(null)
const pendingClearCategory = ref<ClearableCategory | null>(null)
const clearingCategory = ref<ClearableCategory | null>(null)

const CLEAR_BATCH_SIZE = 1000
const CLEAR_PREVIEW_LIMIT = 8

const running = computed(() => runState.value === 'running')
const canStart = computed(
  () =>
    selectedGroupId.value !== null &&
    selectedModelId.value.trim() !== '' &&
    !groupsLoading.value &&
    !modelsLoading.value &&
    clearingCategory.value === null
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

const accountsByClearableCategory = computed<Record<ClearableCategory, StatusCheckAccountResult[]>>(() => {
  const grouped: Record<ClearableCategory, StatusCheckAccountResult[]> = {
    unauthorized: [],
    forbidden: [],
    other_error: []
  }
  for (const result of Object.values(accountResults.value)) {
    if (result.category === 'unauthorized' || result.category === 'forbidden' || result.category === 'other_error') {
      grouped[result.category].push(result)
    }
  }
  for (const accounts of Object.values(grouped)) accounts.sort((left, right) => left.accountId - right.accountId)
  return grouped
})

const pendingClearAccounts = computed(() =>
  pendingClearCategory.value ? accountsByClearableCategory.value[pendingClearCategory.value] : []
)
const pendingClearAccountPreview = computed(() => pendingClearAccounts.value.slice(0, CLEAR_PREVIEW_LIMIT))
const clearDialogTitle = computed(() =>
  pendingClearCategory.value
    ? t('admin.accounts.statusCheck.clearDialogTitle', { category: categoryLabel(pendingClearCategory.value) })
    : ''
)
const clearDialogMessage = computed(() => {
  if (!pendingClearCategory.value || !runSnapshot.value) return ''
  return t('admin.accounts.statusCheck.clearDialogMessage', {
    count: pendingClearAccounts.value.length,
    category: categoryLabel(pendingClearCategory.value),
    group: runSnapshot.value.groupName,
    model: runSnapshot.value.modelId
  })
})
const clearConfirmText = computed(() =>
  t('admin.accounts.statusCheck.clearConfirm', { count: pendingClearAccounts.value.length })
)

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

function terminalLevel(tone: TerminalTone): string {
  return {
    muted: 'INFO ',
    info: 'INFO ',
    success: 'OK   ',
    warning: 'WARN ',
    danger: 'ERROR'
  }[tone]
}

function formatLogTimestamp(date = new Date()): string {
  const pad = (value: number, width = 2) => String(value).padStart(width, '0')
  const day = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
  const time = `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  return `${day} ${time}`
}

async function scrollTerminalToBottom() {
  await nextTick()
  if (terminalRef.value) terminalRef.value.scrollTop = terminalRef.value.scrollHeight
}

function appendTerminal(text: string, tone: TerminalTone = 'muted') {
  const normalized = text.trim()
  if (!normalized) return
  terminalLines.value.push({
    id: ++terminalLineID,
    text: `${formatLogTimestamp()} ${terminalLevel(tone)} ${normalized}`,
    tone
  })
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

function accountBufferKey(event: AccountStatusCheckEvent): number | null {
  return typeof event.account_id === 'number' ? event.account_id : null
}

function appendResponseChunk(event: AccountStatusCheckEvent) {
  const key = accountBufferKey(event)
  if (key === null || !event.text) return
  const current = responseBuffers.get(key) ?? { prefix: accountPrefix(event), text: '' }
  current.text += event.text
  responseBuffers.set(key, current)
}

function formatResponseText(raw: string): string {
  const text = raw.trim()
  if (!text) return ''
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

function appendBufferedResponse(buffered: ResponseBuffer) {
  const content = formatResponseText(buffered.text)
  if (!content) return
  const body = content
    .split(/\r?\n/)
    .map((line) => `  | ${line}`)
    .join('\n')
  appendTerminal(
    `${buffered.prefix} ${t('admin.accounts.statusCheck.log.response')}\n${body}`,
    'muted'
  )
}

function flushResponse(event: AccountStatusCheckEvent) {
  const key = accountBufferKey(event)
  if (key === null) return
  const buffered = responseBuffers.get(key)
  if (!buffered) return
  responseBuffers.delete(key)
  appendBufferedResponse(buffered)
}

function flushAllResponses() {
  for (const buffered of responseBuffers.values()) appendBufferedResponse(buffered)
  responseBuffers.clear()
}

function mergeStats(next?: AccountStatusCheckStats) {
  if (next) stats.value = { ...next }
}

function handleStatusEvent(event: AccountStatusCheckEvent) {
  mergeStats(event.stats)
  switch (event.type) {
    case 'batch_start':
      runSnapshot.value = {
        groupId: event.group_id ?? selectedGroupId.value ?? 0,
        groupName: event.group_name || selectedGroupName.value,
        modelId: event.model_id || selectedModelId.value,
        mode: event.mode || selectedMode.value
      }
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
      if (event.log_type === 'content') {
        appendResponseChunk(event)
      } else {
        if (event.log_type === 'test_complete' || event.log_type === 'error') {
          flushResponse(event)
        }
        appendTerminal(`${accountPrefix(event)} ${event.text || ''}`, event.log_type === 'error' ? 'danger' : 'muted')
      }
      break
    case 'account_result': {
      flushResponse(event)
      if (event.account_id && event.category) {
        accountResults.value = {
          ...accountResults.value,
          [event.account_id]: {
            accountId: event.account_id,
            name: event.account_name || t('admin.accounts.statusCheck.unknownAccount'),
            category: event.category
          }
        }
      }
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
  responseBuffers.clear()
  accountResults.value = {}
  runSnapshot.value = null
  pendingClearCategory.value = null
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
    flushAllResponses()
    runState.value = 'error'
    const message = error instanceof Error ? error.message : t('admin.accounts.statusCheck.requestFailed')
    appendTerminal(t('admin.accounts.statusCheck.log.requestError', { error: message }), 'danger')
  } finally {
    if (abortController === controller) abortController = null
  }
}

function canClearCategory(category: ClearableCategory): boolean {
  return (
    (runState.value === 'completed' || runState.value === 'stopped') &&
    clearingCategory.value === null &&
    runSnapshot.value !== null &&
    accountsByClearableCategory.value[category].length > 0
  )
}

function requestCategoryClear(category: ClearableCategory) {
  if (!canClearCategory(category)) return
  pendingClearCategory.value = category
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  if (error && typeof error === 'object' && 'message' in error) return String(error.message)
  return t('admin.accounts.statusCheck.clearFailed')
}

async function confirmCategoryClear() {
  const category = pendingClearCategory.value
  const snapshot = runSnapshot.value
  const accounts = [...pendingClearAccounts.value]
  if (!category || !snapshot || accounts.length === 0 || clearingCategory.value !== null) return

  pendingClearCategory.value = null
  clearingCategory.value = category
  appendTerminal(
    t('admin.accounts.statusCheck.log.clearStart', {
      category: categoryLabel(category),
      count: accounts.length,
      group: snapshot.groupName
    }),
    'warning'
  )

  const deletedIDs = new Set<number>()
  const requestedIDs = new Set(accounts.map((account) => account.accountId))
  const failures: AccountStatusCheckDeleteFailure[] = []
  try {
    for (let index = 0; index < accounts.length; index += CLEAR_BATCH_SIZE) {
      const chunk = accounts.slice(index, index + CLEAR_BATCH_SIZE)
      try {
        const result = await adminAPI.accounts.deleteStatusCheckAccounts({
          group_id: snapshot.groupId,
          account_ids: chunk.map((account) => account.accountId)
        })
        for (const accountID of result.deleted_ids) {
          if (requestedIDs.has(accountID)) deletedIDs.add(accountID)
        }
        failures.push(...result.failures)
      } catch (error) {
        const message = errorMessage(error)
        failures.push(
          ...chunk.map((account) => ({
            account_id: account.accountId,
            code: 'request_failed',
            message
          }))
        )
      }
    }

    if (deletedIDs.size > 0) {
      const remaining = { ...accountResults.value }
      for (const accountID of deletedIDs) delete remaining[accountID]
      accountResults.value = remaining
      stats.value = {
        ...stats.value,
        total: Math.max(0, stats.value.total - deletedIDs.size),
        completed: Math.max(0, stats.value.completed - deletedIDs.size),
        [category]: Math.max(0, stats.value[category] - deletedIDs.size)
      }
    }

    for (const failure of failures) {
      const account = accountResults.value[failure.account_id]
      appendTerminal(
        t('admin.accounts.statusCheck.log.clearAccountFailed', {
          account: account?.name || `#${failure.account_id}`,
          error: failure.message
        }),
        'danger'
      )
    }

    if (deletedIDs.size === accounts.length) {
      appStore.showSuccess(t('admin.accounts.statusCheck.clearSuccess', { count: deletedIDs.size }))
    } else if (deletedIDs.size > 0) {
      appStore.showWarning(
        t('admin.accounts.statusCheck.clearPartial', { success: deletedIDs.size, failed: failures.length })
      )
    } else {
      appStore.showError(t('admin.accounts.statusCheck.clearFailed'))
    }
    appendTerminal(
      t('admin.accounts.statusCheck.log.clearComplete', {
        success: deletedIDs.size,
        failed: failures.length
      }),
      failures.length > 0 ? 'warning' : 'success'
    )
  } finally {
    clearingCategory.value = null
  }
}

function stopCheck() {
  if (!abortController) return
  abortController.abort()
  abortController = null
  flushAllResponses()
  if (runState.value === 'running') {
    runState.value = 'stopped'
    appendTerminal(t('admin.accounts.statusCheck.log.stopRequested'), 'warning')
  }
}

function cancelOnLeave() {
  abortController?.abort()
  abortController = null
  responseBuffers.clear()
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

<style scoped>
.status-clear-button {
  @apply inline-flex h-8 w-8 items-center justify-center rounded-md text-gray-400;
  @apply transition-colors duration-150 hover:bg-red-50 hover:text-red-600;
  @apply focus:outline-none focus:ring-2 focus:ring-red-500 focus:ring-offset-2;
  @apply disabled:cursor-not-allowed disabled:opacity-35 disabled:hover:bg-transparent disabled:hover:text-gray-400;
  @apply dark:text-gray-500 dark:hover:bg-red-950/40 dark:hover:text-red-300 dark:focus:ring-offset-dark-800;
}
</style>
