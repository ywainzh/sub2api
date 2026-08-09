/**
 * Admin Proxies API endpoints
 * Handles proxy server management for administrators
 */

import { apiClient } from '../client'
import type {
  Proxy,
  ProxyAccountSummary,
  ProxyQualityCheckResult,
  CreateProxyRequest,
  UpdateProxyRequest,
  PaginatedResponse,
  AdminDataPayload,
  AdminDataImportResult
} from '@/types'

// Subscription synchronization also probes every imported node. With the
// backend's three-node concurrency limit, a larger subscription can
// legitimately take several minutes, so the global 30s client timeout is not
// appropriate for these operations.
const OPEN_CODE_PROBE_REQUEST_TIMEOUT_MS = 10 * 60 * 1000

export interface ProxySubscription {
  id: number
  name: string
  enabled: boolean
  sync_interval_minutes: number
  last_fetched_at?: string | null
  last_success_at?: string | null
  last_error?: string
  node_count: number
  last_format?: string
  has_url: boolean
  url_masked: string
  created_at: string
  updated_at: string
	 source_type: 'url' | 'upload'
}

export interface ManagedProxyNode {
  id: number
  subscription_id: number
  proxy_id?: number | null
  node_key: string
  display_name: string
  mihomo_name: string
  protocol: string
  listener_port: number
	 transport_mode: 'mihomo_listener' | 'direct_http'
  sync_status: string
  health_status: string
  last_seen_at?: string | null
  exit_ip?: string
  country?: string
  region?: string
  latency_ms?: number | null
  opencode_http_status?: number | null
  failure_type?: string
  failure_message?: string
  last_probe_at?: string | null
  duplicate_of_node_id?: number | null
	 unavailable_since?: string | null
	 last_successful_probe_at?: string | null
	 consecutive_failures: number
	 retry_at?: string | null
	 retry_count: number
}

export interface OpenCodeNodeProbeResult {
  node_id: number
  success: boolean
  health_status: string
  exit_ip?: string
  country?: string
  region?: string
  latency_ms?: number
  opencode_http_status?: number
  failure_type?: string
  failure_message?: string
  duplicate_of_node_id?: number | null
}

export interface OpenCodeMaintenanceJob {
  id: number
  job_type: 'import' | 'probe'
  trigger_type: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  source_name?: string
  subscription_id?: number | null
  total_nodes: number
  processed_nodes: number
  healthy_nodes: number
  failed_nodes: number
  rate_limited_nodes: number
  duplicate_nodes: number
  deleted_nodes: number
  error_message?: string
  started_at?: string | null
  finished_at?: string | null
  created_at: string
  updated_at: string
}

export interface OpenCodeMaintenanceStatus {
  next_run_at?: string | null
  latest_job?: OpenCodeMaintenanceJob | null
}

export interface OpenCodeModelRegistryStatus {
  ids: string[]
  count: number
  last_fetched_at?: string | null
  last_error?: string
  using_baseline: boolean
}

export interface OpenCodePool {
  id: number
  name: string
  group_id: number
  enabled: boolean
  upstream_key_configured: boolean
  include_server_direct: boolean
  worker_concurrency: number
  reconcile_status: string
  reconcile_error?: string
  last_reconciled_at?: string | null
  active_workers: number
  healthy_nodes: number
  rate_limited_nodes: number
  duplicate_nodes: number
  failed_nodes: number
  probe_interval_minutes: number
  server_direct_status?: string
  created_at: string
  updated_at: string
}

export interface OpenCodePoolWorker {
  id: number
  pool_id: number
  managed_node_id?: number | null
  account_id: number
  proxy_id?: number | null
  display_name: string
  egress_mode: 'proxy' | 'server_direct'
  exit_ip?: string
  status: string
  error_reason?: string
  health_status?: string
  opencode_http_status?: number | null
  last_probe_at?: string | null
  rate_limit_reset_at?: string | null
  created_at: string
  updated_at: string
}

/**
 * List all proxies with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters
 * @returns Paginated list of proxies
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    protocol?: string
    status?: 'active' | 'inactive' | 'expired'
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<PaginatedResponse<Proxy>> {
  const { data } = await apiClient.get<PaginatedResponse<Proxy>>('/admin/proxies', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    signal: options?.signal
  })
  return data
}

/**
 * Get all active proxies (without pagination)
 * @returns List of all active proxies
 */
export async function getAll(): Promise<Proxy[]> {
  const { data } = await apiClient.get<Proxy[]>('/admin/proxies/all')
  return data
}

/**
 * Get all active proxies with account count (sorted by creation time desc)
 * @returns List of all active proxies with account count
 */
export async function getAllWithCount(): Promise<Proxy[]> {
  const { data } = await apiClient.get<Proxy[]>('/admin/proxies/all', {
    params: { with_count: 'true' }
  })
  return data
}

/**
 * Get proxy by ID
 * @param id - Proxy ID
 * @returns Proxy details
 */
export async function getById(id: number): Promise<Proxy> {
  const { data } = await apiClient.get<Proxy>(`/admin/proxies/${id}`)
  return data
}

/**
 * Create new proxy
 * @param proxyData - Proxy data
 * @returns Created proxy
 */
export async function create(proxyData: CreateProxyRequest): Promise<Proxy> {
  const { data } = await apiClient.post<Proxy>('/admin/proxies', proxyData)
  return data
}

/**
 * Update proxy
 * @param id - Proxy ID
 * @param updates - Fields to update
 * @returns Updated proxy
 */
export async function update(id: number, updates: UpdateProxyRequest): Promise<Proxy> {
  const { data } = await apiClient.put<Proxy>(`/admin/proxies/${id}`, updates)
  return data
}

/**
 * Delete proxy
 * @param id - Proxy ID
 * @returns Success confirmation
 */
export async function deleteProxy(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/proxies/${id}`)
  return data
}

/**
 * Toggle proxy status
 * @param id - Proxy ID
 * @param status - New status
 * @returns Updated proxy
 */
export async function toggleStatus(id: number, status: 'active' | 'inactive'): Promise<Proxy> {
  return update(id, { status })
}

/**
 * Test proxy connectivity
 * @param id - Proxy ID
 * @returns Test result with IP info
 */
export async function testProxy(id: number): Promise<{
  success: boolean
  message: string
  latency_ms?: number
  ip_address?: string
  city?: string
  region?: string
  country?: string
  country_code?: string
}> {
  const { data } = await apiClient.post<{
    success: boolean
    message: string
    latency_ms?: number
    ip_address?: string
    city?: string
    region?: string
    country?: string
    country_code?: string
  }>(`/admin/proxies/${id}/test`)
  return data
}

/**
 * Check proxy quality across common AI targets
 * @param id - Proxy ID
 * @returns Quality check result
 */
export async function checkProxyQuality(id: number): Promise<ProxyQualityCheckResult> {
  const { data } = await apiClient.post<ProxyQualityCheckResult>(`/admin/proxies/${id}/quality-check`)
  return data
}

/**
 * Get proxy usage statistics
 * @param id - Proxy ID
 * @returns Proxy usage statistics
 */
export async function getStats(id: number): Promise<{
  total_accounts: number
  active_accounts: number
  total_requests: number
  success_rate: number
  average_latency: number
}> {
  const { data } = await apiClient.get<{
    total_accounts: number
    active_accounts: number
    total_requests: number
    success_rate: number
    average_latency: number
  }>(`/admin/proxies/${id}/stats`)
  return data
}

/**
 * Get accounts using a proxy
 * @param id - Proxy ID
 * @returns List of accounts using the proxy
 */
export async function getProxyAccounts(id: number): Promise<ProxyAccountSummary[]> {
  const { data } = await apiClient.get<ProxyAccountSummary[]>(`/admin/proxies/${id}/accounts`)
  return data
}

/**
 * Batch create proxies
 * @param proxies - Array of proxy data to create
 * @returns Creation result with count of created and skipped
 */
export async function batchCreate(
  proxies: Array<{
    protocol: string
    host: string
    port: number
    username?: string
    password?: string
  }>
): Promise<{
  created: number
  skipped: number
}> {
  const { data } = await apiClient.post<{
    created: number
    skipped: number
  }>('/admin/proxies/batch', { proxies })
  return data
}

export async function batchDelete(ids: number[]): Promise<{
  deleted_ids: number[]
  skipped: Array<{ id: number; reason: string }>
}> {
  const { data } = await apiClient.post<{
    deleted_ids: number[]
    skipped: Array<{ id: number; reason: string }>
  }>('/admin/proxies/batch-delete', { ids })
  return data
}

export async function exportData(options?: {
  ids?: number[]
  filters?: {
    protocol?: string
    status?: 'active' | 'inactive' | 'expired'
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  }
}): Promise<AdminDataPayload> {
  const params: Record<string, string> = {}
  if (options?.ids && options.ids.length > 0) {
    params.ids = options.ids.join(',')
  } else if (options?.filters) {
    const { protocol, status, search, sort_by, sort_order } = options.filters
    if (protocol) params.protocol = protocol
    if (status) params.status = status
    if (search) params.search = search
    if (sort_by) params.sort_by = sort_by
    if (sort_order) params.sort_order = sort_order
  }
  const { data } = await apiClient.get<AdminDataPayload>('/admin/proxies/data', { params })
  return data
}

export async function importData(payload: {
  data: AdminDataPayload
}): Promise<AdminDataImportResult> {
  const { data } = await apiClient.post<AdminDataImportResult>('/admin/proxies/data', payload)
  return data
}

export async function listSubscriptions(): Promise<ProxySubscription[]> {
  const { data } = await apiClient.get<ProxySubscription[]>('/admin/proxy-subscriptions')
  return data
}

export async function createSubscription(input: {
  name: string
  url: string
  enabled?: boolean
  sync_interval_minutes?: number
}): Promise<ProxySubscription> {
  const { data } = await apiClient.post<ProxySubscription>('/admin/proxy-subscriptions', input)
  return data
}

export async function updateSubscription(id: number, input: {
  name: string
  url?: string
  enabled: boolean
  sync_interval_minutes: number
}): Promise<ProxySubscription> {
  const { data } = await apiClient.put<ProxySubscription>(`/admin/proxy-subscriptions/${id}`, input)
  return data
}

export async function deleteSubscription(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/proxy-subscriptions/${id}`)
  return data
}

export async function syncSubscription(id: number): Promise<ManagedProxyNode[]> {
  const { data } = await apiClient.post<ManagedProxyNode[]>(
    `/admin/proxy-subscriptions/${id}/sync`,
    undefined,
    { timeout: OPEN_CODE_PROBE_REQUEST_TIMEOUT_MS }
  )
  return data
}

export async function listSubscriptionNodes(id: number): Promise<ManagedProxyNode[]> {
  const { data } = await apiClient.get<ManagedProxyNode[]>(`/admin/proxy-subscriptions/${id}/nodes`)
  return data
}

export async function probeOpenCodeNodes(nodeIds: number[] = []): Promise<OpenCodeNodeProbeResult[]> {
  const { data } = await apiClient.post<OpenCodeNodeProbeResult[]>(
    '/admin/opencode/proxies/probe',
    { node_ids: nodeIds },
    { timeout: OPEN_CODE_PROBE_REQUEST_TIMEOUT_MS }
  )
  return data
}

export async function createOpenCodeProbeJob(nodeIds: number[] = []): Promise<OpenCodeMaintenanceJob> {
  const { data } = await apiClient.post<OpenCodeMaintenanceJob>('/admin/opencode/proxies/probe-jobs', {
    node_ids: nodeIds
  })
  return data
}

export async function importOpenCodeProxies(file: File, name?: string): Promise<OpenCodeMaintenanceJob> {
  const form = new FormData()
  form.append('file', file)
  if (name) form.append('name', name)
  const { data } = await apiClient.post<OpenCodeMaintenanceJob>('/admin/opencode/proxies/import', form)
  return data
}

export async function getOpenCodeMaintenanceJob(id: number): Promise<OpenCodeMaintenanceJob> {
  const { data } = await apiClient.get<OpenCodeMaintenanceJob>(`/admin/opencode/jobs/${id}`)
  return data
}

export async function getOpenCodeMaintenance(): Promise<OpenCodeMaintenanceStatus> {
  const { data } = await apiClient.get<OpenCodeMaintenanceStatus>('/admin/opencode/maintenance')
  return data
}

export async function deleteOpenCodeManagedNode(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/opencode/proxies/${id}`)
  return data
}

export async function getOpenCodeModels(): Promise<OpenCodeModelRegistryStatus> {
  const { data } = await apiClient.get<OpenCodeModelRegistryStatus>('/admin/opencode/models')
  return data
}

export async function refreshOpenCodeModels(): Promise<OpenCodeModelRegistryStatus> {
  const { data } = await apiClient.post<OpenCodeModelRegistryStatus>('/admin/opencode/models/refresh')
  return data
}

export async function getOpenCodePool(): Promise<OpenCodePool> {
  const { data } = await apiClient.get<OpenCodePool>('/admin/opencode/pool')
  return data
}

export async function updateOpenCodePool(input: {
  enabled?: boolean
  include_server_direct?: boolean
  worker_concurrency?: number
  upstream_api_key?: string
  clear_upstream_api_key?: boolean
}): Promise<OpenCodePool> {
  const { data } = await apiClient.put<OpenCodePool>('/admin/opencode/pool', input)
  return data
}

export async function listOpenCodePoolWorkers(): Promise<OpenCodePoolWorker[]> {
  const { data } = await apiClient.get<OpenCodePoolWorker[]>('/admin/opencode/pool/workers')
  return data
}

export async function reconcileOpenCodePool(): Promise<OpenCodePoolWorker[]> {
  const { data } = await apiClient.post<OpenCodePoolWorker[]>('/admin/opencode/pool/reconcile')
  return data
}

export const proxiesAPI = {
  list,
  getAll,
  getAllWithCount,
  getById,
  create,
  update,
  delete: deleteProxy,
  toggleStatus,
  testProxy,
  checkProxyQuality,
  getStats,
  getProxyAccounts,
  batchCreate,
  batchDelete,
  exportData,
  importData,
  listSubscriptions,
  createSubscription,
  updateSubscription,
  deleteSubscription,
  syncSubscription,
  listSubscriptionNodes,
  probeOpenCodeNodes,
	createOpenCodeProbeJob,
	importOpenCodeProxies,
	getOpenCodeMaintenanceJob,
	getOpenCodeMaintenance,
	deleteOpenCodeManagedNode,
  getOpenCodeModels,
  refreshOpenCodeModels,
  getOpenCodePool,
  updateOpenCodePool,
  listOpenCodePoolWorkers,
  reconcileOpenCodePool
}

export default proxiesAPI
