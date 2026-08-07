<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <div class="flex flex-wrap items-center gap-2 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
      <template v-if="view === 'subscriptions'">
        <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadAll">
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button class="btn btn-primary" @click="openCreate">
          <Icon name="plus" size="md" class="mr-2" />
          {{ t('admin.proxies.openCode.addSubscription') }}
        </button>
        <div class="ml-auto flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
          <span>{{
            t('admin.proxies.openCode.freeModels', {
              count: modelStatus?.count ?? 0
            })
          }}</span>
          <button class="btn btn-secondary btn-sm" :disabled="refreshingModels" @click="refreshModels">
            <Icon name="refresh" size="sm" :class="refreshingModels ? 'animate-spin' : ''" />
          </button>
        </div>
      </template>
      <template v-else>
        <select v-model="healthFilter" class="input w-52" @change="loadAll">
          <option value="">{{ t('admin.proxies.openCode.allHealth') }}</option>
          <option value="healthy">
            {{ t('admin.proxies.openCode.healthy') }}
          </option>
          <option value="rate_limited">429</option>
          <option value="transport_error">
            {{ t('admin.proxies.openCode.transportError') }}
          </option>
          <option value="duplicate_exit">
            {{ t('admin.proxies.openCode.duplicateExit') }}
          </option>
          <option value="unprobed">
            {{ t('admin.proxies.openCode.unprobed') }}
          </option>
        </select>
        <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadAll">
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button class="btn btn-primary" :disabled="probing || nodes.length === 0" @click="probeNodes">
          <Icon name="play" size="md" class="mr-2" />
          {{ t('admin.proxies.openCode.probeAll') }}
        </button>
      </template>
    </div>

    <div class="min-h-0 flex-1 overflow-auto">
      <table v-if="view === 'subscriptions'" class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
        <thead
          class="sticky top-0 bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400"
        >
          <tr>
            <th class="px-4 py-3">{{ t('admin.proxies.openCode.name') }}</th>
            <th class="px-4 py-3">
              {{ t('admin.proxies.openCode.subscriptionUrl') }}
            </th>
            <th class="px-4 py-3">{{ t('admin.proxies.openCode.nodes') }}</th>
            <th class="px-4 py-3">
              {{ t('admin.proxies.openCode.interval') }}
            </th>
            <th class="px-4 py-3">
              {{ t('admin.proxies.openCode.lastSync') }}
            </th>
            <th class="px-4 py-3">{{ t('admin.proxies.columns.status') }}</th>
            <th class="px-4 py-3 text-right">
              {{ t('admin.proxies.columns.actions') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
          <tr v-for="subscription in subscriptions" :key="subscription.id">
            <td class="px-4 py-3 font-medium text-gray-900 dark:text-white">
              {{ subscription.name }}
            </td>
            <td class="px-4 py-3 font-mono text-xs text-gray-500">
              {{ subscription.url_masked }}
            </td>
            <td class="px-4 py-3 text-gray-700 dark:text-gray-200">
              {{ subscription.node_count }}
            </td>
            <td class="px-4 py-3 text-gray-700 dark:text-gray-200">{{ subscription.sync_interval_minutes }} min</td>
            <td class="max-w-80 px-4 py-3">
              <div class="text-gray-700 dark:text-gray-200">
                {{ displayTime(subscription.last_success_at) }}
              </div>
              <div
                v-if="subscription.last_error"
                class="mt-1 truncate text-xs text-red-600"
                :title="subscription.last_error"
              >
                {{ subscription.last_error }}
              </div>
            </td>
            <td class="px-4 py-3">
              <button
                class="badge"
                :class="subscription.enabled ? 'badge-success' : 'badge-gray'"
                @click="toggleSubscription(subscription)"
              >
                {{ subscription.enabled ? t('admin.accounts.status.active') : t('admin.accounts.status.inactive') }}
              </button>
            </td>
            <td class="px-4 py-3">
              <div class="flex justify-end gap-1">
                <button
                  class="btn btn-ghost btn-sm"
                  :disabled="syncingIds.has(subscription.id)"
                  :title="t('admin.proxies.openCode.sync')"
                  @click="syncOne(subscription.id)"
                >
                  <Icon name="refresh" size="sm" :class="syncingIds.has(subscription.id) ? 'animate-spin' : ''" />
                </button>
                <button class="btn btn-ghost btn-sm" :title="t('common.edit')" @click="openEdit(subscription)">
                  <Icon name="edit" size="sm" />
                </button>
                <button
                  class="btn btn-ghost btn-sm text-red-600"
                  :title="t('common.delete')"
                  @click="removeSubscription(subscription)"
                >
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>

      <table v-else class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
        <thead
          class="sticky top-0 bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400"
        >
          <tr>
            <th class="px-4 py-3">{{ t('admin.proxies.openCode.name') }}</th>
            <th class="px-4 py-3">{{ t('admin.proxies.columns.protocol') }}</th>
            <th class="px-4 py-3">{{ t('admin.proxies.openCode.exitIp') }}</th>
            <th class="px-4 py-3">{{ t('admin.proxies.openCode.region') }}</th>
            <th class="px-4 py-3">{{ t('admin.proxies.columns.latency') }}</th>
            <th class="px-4 py-3">HTTP</th>
            <th class="px-4 py-3">{{ t('admin.proxies.columns.status') }}</th>
            <th class="px-4 py-3">
              {{ t('admin.proxies.openCode.lastProbe') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
          <tr v-for="node in filteredNodes" :key="node.id">
            <td class="px-4 py-3">
              <div class="font-medium text-gray-900 dark:text-white">
                {{ node.display_name }}
              </div>
              <div class="mt-0.5 font-mono text-xs text-gray-400">
                #{{ node.proxy_id ?? '-' }} · :{{ node.listener_port }}
              </div>
            </td>
            <td class="px-4 py-3">
              <span class="badge badge-gray">{{ node.protocol.toUpperCase() }}</span>
            </td>
            <td class="px-4 py-3 font-mono text-xs text-gray-700 dark:text-gray-200">
              {{ node.exit_ip || '-' }}
            </td>
            <td class="px-4 py-3 text-xs text-gray-700 dark:text-gray-200">
              {{ [node.country, node.region].filter(Boolean).join(' / ') || '-' }}
            </td>
            <td class="px-4 py-3">
              {{ node.latency_ms != null ? `${node.latency_ms} ms` : '-' }}
            </td>
            <td class="px-4 py-3">{{ node.opencode_http_status ?? '-' }}</td>
            <td class="px-4 py-3">
              <span class="badge" :class="healthClass(node.health_status)" :title="node.failure_message || undefined">
                {{ healthLabel(node.health_status) }}
              </span>
            </td>
            <td class="px-4 py-3 text-xs text-gray-500">
              {{ displayTime(node.last_probe_at) }}
            </td>
          </tr>
        </tbody>
      </table>

      <div
        v-if="
          !loading &&
          ((view === 'subscriptions' && subscriptions.length === 0) || (view === 'nodes' && filteredNodes.length === 0))
        "
        class="py-16 text-center text-sm text-gray-500"
      >
        {{ t('common.noData') }}
      </div>
    </div>

    <BaseDialog
      :show="showDialog"
      :title="editing ? t('admin.proxies.openCode.editSubscription') : t('admin.proxies.openCode.addSubscription')"
      width="normal"
      @close="closeDialog"
    >
      <form
        id="opencode-subscription-form"
        class="space-y-5"
        autocomplete="off"
        data-form-type="other"
        @submit.prevent="saveSubscription"
      >
        <div>
          <label class="input-label">{{ t('admin.proxies.openCode.name') }}</label>
          <input v-model="form.name" class="input" type="text" required maxlength="100" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.openCode.subscriptionUrl') }}</label>
          <input
            v-model="form.url"
            class="input font-mono"
            type="password"
            name="opencode-subscription-secret"
            autocomplete="new-password"
            data-1p-ignore
            data-lpignore="true"
            data-bwignore="true"
            :required="!editing"
            :placeholder="editing ? t('admin.proxies.openCode.keepUrl') : 'https://...'"
          />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.openCode.interval') }}</label>
          <input v-model.number="form.sync_interval_minutes" class="input" type="number" min="5" max="43200" required />
        </div>
        <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
          <input v-model="form.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" />
          {{ t('admin.accounts.status.active') }}
        </label>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="btn btn-secondary" @click="closeDialog">
            {{ t('common.cancel') }}
          </button>
          <button class="btn btn-primary" type="submit" form="opencode-subscription-form" :disabled="saving">
            {{ t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { ManagedProxyNode, OpenCodeModelRegistryStatus, ProxySubscription } from '@/api/admin/proxies'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ view: 'subscriptions' | 'nodes' }>()
const { t } = useI18n()
const appStore = useAppStore()
const subscriptions = ref<ProxySubscription[]>([])
const nodes = ref<ManagedProxyNode[]>([])
const modelStatus = ref<OpenCodeModelRegistryStatus | null>(null)
const loading = ref(false)
const probing = ref(false)
const refreshingModels = ref(false)
const syncingIds = ref(new Set<number>())
const healthFilter = ref('')
const showDialog = ref(false)
const editing = ref<ProxySubscription | null>(null)
const saving = ref(false)
const form = reactive({
  name: '',
  url: '',
  enabled: true,
  sync_interval_minutes: 360
})

const errorMessage = (error: unknown) =>
  error && typeof error === 'object' && 'message' in error ? String(error.message) : String(error)
const filteredNodes = computed(() =>
  healthFilter.value ? nodes.value.filter((node) => node.health_status === healthFilter.value) : nodes.value
)
const displayTime = (value?: string | null) => (value ? new Date(value).toLocaleString() : '-')
const healthClass = (status: string) =>
  status === 'healthy'
    ? 'badge-success'
    : status === 'rate_limited' || status === 'duplicate_exit'
      ? 'badge-warning'
      : status === 'unprobed'
        ? 'badge-gray'
        : 'badge-danger'
const healthLabel = (status: string) =>
  ({
    healthy: t('admin.proxies.openCode.healthy'),
    rate_limited: '429',
    duplicate_exit: t('admin.proxies.openCode.duplicateExit'),
    transport_error: t('admin.proxies.openCode.transportError'),
    unprobed: t('admin.proxies.openCode.unprobed'),
    missing: t('admin.proxies.openCode.missing')
  })[status] || status

async function loadAll() {
  loading.value = true
  try {
    subscriptions.value = await adminAPI.proxies.listSubscriptions()
    if (props.view === 'nodes') {
      const batches = await Promise.all(
        subscriptions.value.map((item) => adminAPI.proxies.listSubscriptionNodes(item.id))
      )
      nodes.value = batches.flat()
    } else {
      modelStatus.value = await adminAPI.proxies.getOpenCodeModels()
    }
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  Object.assign(form, {
    name: '',
    url: '',
    enabled: true,
    sync_interval_minutes: 360
  })
  showDialog.value = true
}

function openEdit(item: ProxySubscription) {
  editing.value = item
  Object.assign(form, {
    name: item.name,
    url: '',
    enabled: item.enabled,
    sync_interval_minutes: item.sync_interval_minutes
  })
  showDialog.value = true
}

function closeDialog() {
  showDialog.value = false
}

async function saveSubscription() {
  saving.value = true
  try {
    if (editing.value) {
      await adminAPI.proxies.updateSubscription(editing.value.id, {
        name: form.name.trim(),
        url: form.url.trim() || undefined,
        enabled: form.enabled,
        sync_interval_minutes: form.sync_interval_minutes
      })
    } else {
      const created = await adminAPI.proxies.createSubscription({
        name: form.name.trim(),
        url: form.url.trim(),
        enabled: form.enabled,
        sync_interval_minutes: form.sync_interval_minutes
      })
      if (created.enabled) await adminAPI.proxies.syncSubscription(created.id)
    }
    closeDialog()
    await loadAll()
    appStore.showSuccess(t('common.saved'))
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    saving.value = false
  }
}

async function syncOne(id: number) {
  syncingIds.value = new Set(syncingIds.value).add(id)
  try {
    await adminAPI.proxies.syncSubscription(id)
    await loadAll()
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    const next = new Set(syncingIds.value)
    next.delete(id)
    syncingIds.value = next
  }
}

async function toggleSubscription(item: ProxySubscription) {
  try {
    await adminAPI.proxies.updateSubscription(item.id, {
      name: item.name,
      enabled: !item.enabled,
      sync_interval_minutes: item.sync_interval_minutes
    })
    await loadAll()
  } catch (error) {
    appStore.showError(errorMessage(error))
  }
}

async function removeSubscription(item: ProxySubscription) {
  if (!window.confirm(t('admin.proxies.openCode.deleteConfirm', { name: item.name }))) return
  try {
    await adminAPI.proxies.deleteSubscription(item.id)
    await loadAll()
  } catch (error) {
    appStore.showError(errorMessage(error))
  }
}

async function probeNodes() {
  probing.value = true
  try {
    await adminAPI.proxies.probeOpenCodeNodes(nodes.value.map((node) => node.id))
    await loadAll()
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    probing.value = false
  }
}

async function refreshModels() {
  refreshingModels.value = true
  try {
    modelStatus.value = await adminAPI.proxies.refreshOpenCodeModels()
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    refreshingModels.value = false
  }
}

watch(() => props.view, loadAll)
onMounted(loadAll)
</script>
