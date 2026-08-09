<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <section v-if="pool" class="border-b border-gray-200 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-800/40">
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-6">
        <div class="rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.openCode.poolStatus') }}</div>
          <div class="mt-1 flex items-center gap-2">
            <span class="badge" :class="pool.enabled ? 'badge-success' : 'badge-gray'">
              {{ pool.enabled ? t('admin.accounts.status.active') : t('admin.accounts.status.inactive') }}
            </span>
            <span class="text-xs text-gray-500">{{ pool.reconcile_status }}</span>
          </div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.openCode.activeWorkers') }}</div>
          <div class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">{{ pool.active_workers }}</div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.openCode.healthy') }}</div>
          <div class="mt-1 text-xl font-semibold text-emerald-600">{{ pool.healthy_nodes }}</div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900">
          <div class="text-xs text-gray-500 dark:text-gray-400">
            429 / {{ t('admin.proxies.openCode.duplicateExit') }} / {{ t('admin.proxies.openCode.otherFailures') }}
          </div>
          <div class="mt-1 flex items-center text-xl font-semibold text-amber-600">
            <button
              type="button"
              class="inline-flex h-11 min-w-11 items-center justify-center rounded-lg px-2 transition-colors duration-200 hover:bg-amber-100 focus:outline-none focus:ring-2 focus:ring-amber-500 focus:ring-offset-1 disabled:cursor-default disabled:opacity-50 dark:hover:bg-amber-900/30 dark:focus:ring-offset-dark-900"
              :class="healthFilter === 'rate_limited' ? 'bg-amber-100 ring-1 ring-amber-300 dark:bg-amber-900/30 dark:ring-amber-700' : ''"
              :disabled="pool.rate_limited_nodes === 0"
              :title="t('admin.proxies.openCode.viewNodesByStatus', { status: '429', count: pool.rate_limited_nodes })"
              data-testid="opencode-filter-rate-limited"
              @click="showNodeFilter('rate_limited')"
            >
              {{ pool.rate_limited_nodes }}
            </button>
            <span aria-hidden="true">/</span>
            <button
              type="button"
              class="inline-flex h-11 min-w-11 items-center justify-center rounded-lg px-2 transition-colors duration-200 hover:bg-amber-100 focus:outline-none focus:ring-2 focus:ring-amber-500 focus:ring-offset-1 disabled:cursor-default disabled:opacity-50 dark:hover:bg-amber-900/30 dark:focus:ring-offset-dark-900"
              :class="healthFilter === 'duplicate_exit' ? 'bg-amber-100 ring-1 ring-amber-300 dark:bg-amber-900/30 dark:ring-amber-700' : ''"
              :disabled="pool.duplicate_nodes === 0"
              :title="t('admin.proxies.openCode.viewNodesByStatus', { status: t('admin.proxies.openCode.duplicateExit'), count: pool.duplicate_nodes })"
              data-testid="opencode-filter-duplicate"
              @click="showNodeFilter('duplicate_exit')"
            >
              {{ pool.duplicate_nodes }}
            </button>
            <span aria-hidden="true">/</span>
            <button
              type="button"
              class="inline-flex h-11 min-w-11 items-center justify-center rounded-lg px-2 transition-colors duration-200 hover:bg-amber-100 focus:outline-none focus:ring-2 focus:ring-amber-500 focus:ring-offset-1 disabled:cursor-default disabled:opacity-50 dark:hover:bg-amber-900/30 dark:focus:ring-offset-dark-900"
              :class="healthFilter === 'failed' ? 'bg-amber-100 ring-1 ring-amber-300 dark:bg-amber-900/30 dark:ring-amber-700' : ''"
              :disabled="pool.failed_nodes === 0"
              :title="t('admin.proxies.openCode.viewNodesByStatus', { status: t('admin.proxies.openCode.otherFailures'), count: pool.failed_nodes })"
              data-testid="opencode-filter-failed"
              @click="showNodeFilter('failed')"
            >
              {{ pool.failed_nodes }}
            </button>
          </div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.openCode.upstreamKey') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">
            {{ pool.upstream_key_configured ? t('admin.proxies.openCode.keyConfigured') : t('admin.proxies.openCode.keyless') }}
          </div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900">
          <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.proxies.openCode.serverDirect') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">
            {{ pool.include_server_direct ? (pool.server_direct_status || t('admin.proxies.openCode.pending')) : t('admin.proxies.openCode.disabled') }}
          </div>
        </div>
      </div>

      <p v-if="pool.reconcile_error" class="mt-2 text-xs text-red-600" role="alert">{{ pool.reconcile_error }}</p>
    </section>

    <div class="flex flex-wrap items-center gap-2 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
      <template v-if="view === 'subscriptions'">
        <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadAll">
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button class="btn btn-primary" @click="openCreate">
          <Icon name="plus" size="md" class="mr-2" />
          {{ t('admin.proxies.openCode.addSubscription') }}
        </button>
		<input ref="importFileInput" type="file" accept=".txt,text/plain" class="hidden" @change="importProxyFile" />
		<button class="btn btn-secondary" :disabled="importing" type="button" @click="importFileInput?.click()">
		  <Icon :name="importing ? 'refresh' : 'upload'" size="md" class="mr-2" :class="importing ? 'animate-spin' : ''" />
		  {{ importing ? t('admin.proxies.openCode.importing') : t('admin.proxies.openCode.importNodes') }}
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
        <select v-model="healthFilter" class="input w-52" @change="handleHealthFilterChange">
          <option value="">{{ t('admin.proxies.openCode.allHealth') }}</option>
          <option value="healthy">
            {{ t('admin.proxies.openCode.healthy') }}
          </option>
          <option value="rate_limited">429</option>
          <option value="failed">
            {{ t('admin.proxies.openCode.otherFailures') }}
          </option>
          <option value="transport_error">
            {{ t('admin.proxies.openCode.transportError') }}
          </option>
          <option value="duplicate_exit">
            {{ t('admin.proxies.openCode.duplicateExit') }}
          </option>
          <option value="unprobed">
            {{ t('admin.proxies.openCode.unprobed') }}
          </option>
		  <option value="quarantined">{{ t('admin.proxies.openCode.quarantined') }}</option>
        </select>
        <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="loadAll">
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button
          class="btn btn-primary"
          type="button"
          data-testid="opencode-probe-all"
          :disabled="probing || probeableNodes.length === 0"
          :aria-busy="probing"
          @click="probeNodes"
        >
          <Icon :name="probing ? 'refresh' : 'play'" size="md" class="mr-2" :class="probing ? 'animate-spin' : ''" />
          {{ probing ? t('admin.proxies.openCode.probing') : t('admin.proxies.openCode.probeAll') }}
        </button>
        <div class="ml-auto flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
          <Icon name="clock" size="sm" />
		  <span>{{ t('admin.proxies.openCode.dailyMaintenanceHint', { time: displayTime(maintenance?.next_run_at) }) }}</span>
        </div>
      </template>
    </div>

    <div
	  v-if="probing"
      class="border-b border-primary-200 bg-primary-50/80 px-4 py-3 text-primary-900 dark:border-primary-800 dark:bg-primary-900/20 dark:text-primary-100"
      role="status"
      aria-live="polite"
      data-testid="opencode-probe-progress"
    >
      <div class="flex items-start gap-3">
        <span class="mt-0.5 rounded-full bg-primary-100 p-2 text-primary-700 dark:bg-primary-900/60 dark:text-primary-300">
          <Icon name="refresh" size="sm" class="animate-spin" />
        </span>
        <div class="min-w-0">
          <p class="text-sm font-medium">
			{{ activeJob ? t('admin.proxies.openCode.jobProgress', { processed: activeJob.processed_nodes, total: activeJob.total_nodes }) : t('admin.proxies.openCode.probeInProgress', { count: probingNodeIds.size }) }}
          </p>
          <p class="mt-0.5 text-xs text-primary-700 dark:text-primary-300">
            {{ t('admin.proxies.openCode.probeProgressHint', { seconds: probeElapsedSeconds }) }}
          </p>
        </div>
      </div>
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
          <tr v-for="subscription in paginatedSubscriptions" :key="subscription.id">
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
				  v-if="subscription.source_type === 'url'"
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
            <th class="px-4 py-3">{{ t('admin.proxies.openCode.worker') }}</th>
            <th class="px-4 py-3">
              {{ t('admin.proxies.openCode.lastProbe') }}
            </th>
            <th class="px-4 py-3">
              {{ t('admin.proxies.columns.actions') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
          <tr
            v-for="node in paginatedNodes"
            :key="node.id"
            :aria-busy="probingNodeIds.has(node.id)"
            :class="probingNodeIds.has(node.id) ? 'bg-primary-50/40 dark:bg-primary-900/10' : ''"
          >
            <td class="px-4 py-3">
              <div class="font-medium text-gray-900 dark:text-white">
                {{ node.display_name }}
              </div>
              <div class="mt-0.5 font-mono text-xs text-gray-400">
				#{{ node.proxy_id ?? '-' }} · {{ node.transport_mode === 'direct_http' ? t('admin.proxies.openCode.directHttp') : `:${node.listener_port}` }}
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
              <span v-if="probingNodeIds.has(node.id)" class="badge badge-primary" data-testid="opencode-node-probing">
                <Icon name="refresh" size="xs" class="animate-spin" />
                {{ t('admin.proxies.openCode.probing') }}
              </span>
              <span
                v-else
                class="badge"
                :class="healthClass(node.health_status)"
                :title="node.failure_message || undefined"
              >
                {{ healthLabel(node.health_status) }}
              </span>
            </td>
            <td class="px-4 py-3">
              <template v-if="workerByNodeId.get(node.id)">
                <span class="badge" :class="workerClass(workerByNodeId.get(node.id)!.status)">
                  {{ workerLabel(workerByNodeId.get(node.id)!.status) }}
                </span>
                <div class="mt-1 font-mono text-xs text-gray-400">#{{ workerByNodeId.get(node.id)!.account_id }}</div>
              </template>
              <span v-else class="text-xs text-gray-400">-</span>
            </td>
            <td class="px-4 py-3 text-xs text-gray-500">
              {{ displayTime(node.last_probe_at) }}
			  <div v-if="node.consecutive_failures > 0" class="mt-1 text-red-500">
				{{ t('admin.proxies.openCode.failureCount', { count: node.consecutive_failures }) }}
			  </div>
			  <div v-if="node.retry_at" class="mt-1 text-amber-600">
				{{ t('admin.proxies.openCode.retryAt', { time: displayTime(node.retry_at) }) }}
			  </div>
            </td>
            <td class="px-4 py-2">
              <div class="flex items-center gap-1 whitespace-nowrap">
                <button
                  type="button"
                  class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-emerald-50 hover:text-emerald-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-emerald-900/20 dark:hover:text-emerald-400"
                  :disabled="probing || !isPoolNode(node)"
                  :aria-busy="probingNodeIds.has(node.id)"
                  :title="t('admin.proxies.openCode.probeNode')"
                  :data-testid="`opencode-probe-node-${node.id}`"
                  @click="probeNode(node)"
                >
                  <Icon
                    name="refresh"
                    size="sm"
                    :class="probingNodeIds.has(node.id) ? 'animate-spin' : ''"
                  />
                  <span class="text-xs">
                    {{
                      probingNodeIds.has(node.id)
                        ? t('admin.proxies.openCode.probing')
                        : t('admin.proxies.openCode.probe')
                    }}
                  </span>
                </button>
                <button
                  type="button"
                  class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                  :disabled="probing || deletingNodeIds.has(node.id)"
                  :title="t('admin.proxies.openCode.deleteNode')"
                  :data-testid="`opencode-delete-node-${node.id}`"
                  @click="deleteNode(node)"
                >
                  <Icon name="trash" size="sm" />
                  <span class="text-xs">
                    {{ deletingNodeIds.has(node.id) ? t('common.processing') : t('common.delete') }}
                  </span>
                </button>
              </div>
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

    <Pagination
      v-if="view === 'subscriptions' && subscriptions.length > 0"
      data-testid="opencode-subscription-pagination"
      :page="subscriptionPagination.page"
      :page-size="subscriptionPagination.page_size"
      :total="subscriptions.length"
      :show-jump="true"
      @update:page="handleSubscriptionPageChange"
      @update:page-size="handlePageSizeChange"
    />
    <Pagination
      v-else-if="view === 'nodes' && filteredNodes.length > 0"
      data-testid="opencode-node-pagination"
      :page="nodePagination.page"
      :page-size="nodePagination.page_size"
      :total="filteredNodes.length"
      :show-jump="true"
      @update:page="handleNodePageChange"
      @update:page-size="handlePageSizeChange"
    />

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
		<div v-if="!editing || editing.source_type === 'url'">
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
		<div v-if="!editing || editing.source_type === 'url'">
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
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type {
  ManagedProxyNode,
	OpenCodeMaintenanceJob,
	OpenCodeMaintenanceStatus,
  OpenCodeModelRegistryStatus,
  OpenCodeNodeProbeResult,
  OpenCodePool,
  OpenCodePoolWorker,
  ProxySubscription
} from '@/api/admin/proxies'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'

const props = defineProps<{ view: 'subscriptions' | 'nodes' }>()
const emit = defineEmits<{ showNodes: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const subscriptions = ref<ProxySubscription[]>([])
const nodes = ref<ManagedProxyNode[]>([])
const modelStatus = ref<OpenCodeModelRegistryStatus | null>(null)
const pool = ref<OpenCodePool | null>(null)
const workers = ref<OpenCodePoolWorker[]>([])
const loading = ref(false)
const probing = ref(false)
const probingNodeIds = ref(new Set<number>())
	const deletingNodeIds = ref(new Set<number>())
const probeElapsedSeconds = ref(0)
	const activeJob = ref<OpenCodeMaintenanceJob | null>(null)
	const maintenance = ref<OpenCodeMaintenanceStatus | null>(null)
	const importing = ref(false)
	const importFileInput = ref<HTMLInputElement | null>(null)
const refreshingModels = ref(false)
const syncingIds = ref(new Set<number>())
const initialPageSize = getPersistedPageSize()
const subscriptionPagination = reactive({ page: 1, page_size: initialPageSize })
const nodePagination = reactive({ page: 1, page_size: initialPageSize })
type NodeHealthFilter = '' | 'healthy' | 'rate_limited' | 'duplicate_exit' | 'failed' | 'transport_error' | 'unprobed' | 'quarantined'
const healthFilter = ref<NodeHealthFilter>('')
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
const enabledSubscriptionIds = computed(
  () => new Set(subscriptions.value.filter((subscription) => subscription.enabled).map((subscription) => subscription.id))
)
const isPoolNode = (node: ManagedProxyNode) =>
  enabledSubscriptionIds.value.has(node.subscription_id) && node.sync_status === 'active'
const probeableNodes = computed(() => nodes.value.filter(isPoolNode))
const isOtherFailedNode = (node: ManagedProxyNode) =>
  isPoolNode(node) &&
  !['healthy', 'rate_limited', 'duplicate_exit'].includes(node.health_status)
const filteredNodes = computed(() => {
  if (!healthFilter.value) return nodes.value
  if (healthFilter.value === 'failed') return nodes.value.filter(isOtherFailedNode)
  if (healthFilter.value === 'duplicate_exit') {
    return nodes.value.filter(
      (node) => isPoolNode(node) && (node.health_status === 'duplicate_exit' || node.duplicate_of_node_id != null)
    )
  }
  if (healthFilter.value === 'healthy') {
    return nodes.value.filter(
      (node) => isPoolNode(node) && node.health_status === 'healthy' && node.duplicate_of_node_id == null
    )
  }
  if (healthFilter.value === 'rate_limited') {
    return nodes.value.filter((node) => isPoolNode(node) && node.health_status === healthFilter.value)
  }
  return nodes.value.filter((node) => node.health_status === healthFilter.value)
})
function paginate<T>(items: T[], page: number, pageSize: number) {
  const start = (page - 1) * pageSize
  return items.slice(start, start + pageSize)
}
const paginatedSubscriptions = computed(() =>
  paginate(subscriptions.value, subscriptionPagination.page, subscriptionPagination.page_size)
)
const paginatedNodes = computed(() =>
  paginate(filteredNodes.value, nodePagination.page, nodePagination.page_size)
)
const workerByNodeId = computed(() => {
  const mapped = new Map<number, OpenCodePoolWorker>()
  for (const worker of workers.value) {
    if (worker.managed_node_id) mapped.set(worker.managed_node_id, worker)
  }
  return mapped
})
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
    auth_error: t('admin.proxies.openCode.authError'),
    http_error: t('admin.proxies.openCode.httpError'),
    unprobed: t('admin.proxies.openCode.unprobed'),
    missing: t('admin.proxies.openCode.missing'),
	quarantined: t('admin.proxies.openCode.quarantined')
  })[status] || status

function showNodeFilter(filter: Exclude<NodeHealthFilter, '' | 'healthy' | 'transport_error' | 'unprobed'>) {
  healthFilter.value = filter
  nodePagination.page = 1
  if (props.view !== 'nodes') emit('showNodes')
}

function clampPage(total: number, pagination: { page: number; page_size: number }) {
  const lastPage = Math.max(1, Math.ceil(total / pagination.page_size))
  if (pagination.page > lastPage) pagination.page = lastPage
}

function handleSubscriptionPageChange(page: number) {
  subscriptionPagination.page = page
}

function handleNodePageChange(page: number) {
  nodePagination.page = page
}

function handlePageSizeChange(pageSize: number) {
  subscriptionPagination.page_size = pageSize
  subscriptionPagination.page = 1
  nodePagination.page_size = pageSize
  nodePagination.page = 1
}

function handleHealthFilterChange() {
  nodePagination.page = 1
}
const workerClass = (status: string) =>
  status === 'active' ? 'badge-success' : status === 'cooling' ? 'badge-warning' : 'badge-gray'
const workerLabel = (status: string) =>
  ({
    active: t('admin.proxies.openCode.workerActive'),
    cooling: t('admin.proxies.openCode.workerCooling'),
    inactive: t('admin.proxies.openCode.workerInactive')
  })[status] || status

let probeStartedAt = 0
let probeElapsedTimer: number | undefined
let disposed = false

function startProbeProgress(nodeIds: number[]) {
  if (probeElapsedTimer !== undefined) window.clearInterval(probeElapsedTimer)
  probeStartedAt = Date.now()
  probeElapsedSeconds.value = 0
  probingNodeIds.value = new Set(nodeIds)
  probeElapsedTimer = window.setInterval(() => {
    updateProbeElapsed()
  }, 1000)
}

function updateProbeElapsed() {
  if (probeStartedAt > 0) {
    probeElapsedSeconds.value = Math.floor((Date.now() - probeStartedAt) / 1000)
  }
}

function stopProbeProgress() {
  if (probeElapsedTimer !== undefined) {
    window.clearInterval(probeElapsedTimer)
    probeElapsedTimer = undefined
  }
  updateProbeElapsed()
  probeStartedAt = 0
  probingNodeIds.value = new Set()
}

function summarizeProbeResults(results: OpenCodeNodeProbeResult[]) {
  const healthy = results.filter((result) => result.health_status === 'healthy').length
  const rateLimited = results.filter((result) => result.health_status === 'rate_limited').length
  const duplicate = results.filter((result) => result.health_status === 'duplicate_exit').length
  return {
    healthy,
    rateLimited,
    duplicate,
    failed: Math.max(0, results.length - healthy - rateLimited - duplicate)
  }
}

async function loadAll() {
  loading.value = true
  try {
	const [subscriptionItems, poolStatus, poolWorkers, maintenanceStatus] = await Promise.all([
      adminAPI.proxies.listSubscriptions(),
      adminAPI.proxies.getOpenCodePool(),
	  adminAPI.proxies.listOpenCodePoolWorkers(),
	  adminAPI.proxies.getOpenCodeMaintenance()
    ])
    subscriptions.value = subscriptionItems
    pool.value = poolStatus
    workers.value = poolWorkers
	maintenance.value = maintenanceStatus
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
      closeDialog()
      appStore.showSuccess(t('common.saved'))
      await loadAll()
    } else {
      const created = await adminAPI.proxies.createSubscription({
        name: form.name.trim(),
        url: form.url.trim(),
        enabled: form.enabled,
        sync_interval_minutes: form.sync_interval_minutes
      })
      subscriptions.value = [...subscriptions.value, created]
      closeDialog()
      appStore.showSuccess(t('common.saved'))
      if (created.enabled) {
        void syncCreatedSubscription(created.id)
      } else {
        await loadAll()
      }
    }
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    saving.value = false
  }
}

async function syncCreatedSubscription(id: number) {
  syncingIds.value = new Set(syncingIds.value).add(id)
  try {
    await adminAPI.proxies.syncSubscription(id)
  } catch (error) {
    appStore.showWarning(
      t('admin.proxies.openCode.savedSyncFailed', {
        error: errorMessage(error)
      }),
      6000
    )
  } finally {
    const next = new Set(syncingIds.value)
    next.delete(id)
    syncingIds.value = next
    await loadAll()
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

async function runProbe(nodeIds: number[]) {
  if (probing.value || nodeIds.length === 0) return
  probing.value = true
  startProbeProgress(nodeIds)
  try {
    const results = await adminAPI.proxies.probeOpenCodeNodes(nodeIds)
    await loadAll()
    updateProbeElapsed()
    const summary = summarizeProbeResults(results)
    appStore.showSuccess(
      t('admin.proxies.openCode.probeCompleted', {
        ...summary,
        seconds: probeElapsedSeconds.value
      }),
      6000
    )
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    stopProbeProgress()
    probing.value = false
  }
}

async function probeNodes() {
	if (probing.value || probeableNodes.value.length === 0) return
	probing.value = true
	startProbeProgress(probeableNodes.value.map((node) => node.id))
	try {
		activeJob.value = await adminAPI.proxies.createOpenCodeProbeJob(probeableNodes.value.map((node) => node.id))
		await pollMaintenanceJob(activeJob.value.id)
	} catch (error) {
		appStore.showError(errorMessage(error))
	} finally {
		stopProbeProgress()
		probing.value = false
		activeJob.value = null
	}
}

async function probeNode(node: ManagedProxyNode) {
  if (!isPoolNode(node)) return
  await runProbe([node.id])
}

async function pollMaintenanceJob(jobId: number) {
  while (true) {
	if (disposed) return
		const job = await adminAPI.proxies.getOpenCodeMaintenanceJob(jobId)
		activeJob.value = job
		if (job.status === 'completed' || job.status === 'failed') {
			await loadAll()
			if (job.status === 'failed') throw new Error(job.error_message || t('admin.proxies.openCode.jobFailed'))
			appStore.showSuccess(t('admin.proxies.openCode.jobCompleted', {
				healthy: job.healthy_nodes,
				failed: job.failed_nodes,
				rateLimited: job.rate_limited_nodes,
				duplicate: job.duplicate_nodes
			}), 6000)
			return
		}
		await new Promise((resolve) => window.setTimeout(resolve, 2000))
	}
}

async function initialLoad() {
	await loadAll()
	const latest = maintenance.value?.latest_job
	if (!latest || (latest.status !== 'pending' && latest.status !== 'running')) return
	probing.value = true
	activeJob.value = latest
	startProbeProgress([])
	try {
		await pollMaintenanceJob(latest.id)
	} catch (error) {
		appStore.showError(errorMessage(error))
	} finally {
		probing.value = false
		activeJob.value = null
		stopProbeProgress()
	}
}

async function importProxyFile(event: Event) {
	const input = event.target as HTMLInputElement
	const file = input.files?.[0]
	if (!file || importing.value) return
	importing.value = true
	probing.value = true
	startProbeProgress([])
	try {
		activeJob.value = await adminAPI.proxies.importOpenCodeProxies(file)
		await pollMaintenanceJob(activeJob.value.id)
	} catch (error) {
		appStore.showError(errorMessage(error))
	} finally {
		input.value = ''
		importing.value = false
		probing.value = false
		activeJob.value = null
		stopProbeProgress()
	}
}

async function deleteNode(node: ManagedProxyNode) {
	if (!window.confirm(t('admin.proxies.openCode.deleteNodeConfirm', { name: node.display_name }))) return
	deletingNodeIds.value = new Set(deletingNodeIds.value).add(node.id)
	try {
		await adminAPI.proxies.deleteOpenCodeManagedNode(node.id)
		appStore.showSuccess(t('admin.proxies.openCode.nodeDeleted'))
		await loadAll()
	} catch (error) {
		appStore.showError(errorMessage(error))
	} finally {
		const next = new Set(deletingNodeIds.value)
		next.delete(node.id)
		deletingNodeIds.value = next
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

watch(() => subscriptions.value.length, (total) => clampPage(total, subscriptionPagination))
watch(() => filteredNodes.value.length, (total) => clampPage(total, nodePagination))
watch(healthFilter, () => {
  nodePagination.page = 1
})
watch(() => props.view, loadAll)
onMounted(initialLoad)
onUnmounted(() => {
	disposed = true
	stopProbeProgress()
})
</script>
