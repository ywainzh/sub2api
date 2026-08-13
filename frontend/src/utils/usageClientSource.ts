import type { UsageClientSource } from '@/types'

export function normalizeUsageClientSource(source?: string | null): UsageClientSource {
  if (source === 'codex' || source === 'claude' || source === 'pi') return source
  return 'unknown'
}

export function getUsageClientSourceLabel(
  source: string | null | undefined,
  translate: (key: string) => string,
): string {
  switch (normalizeUsageClientSource(source)) {
    case 'codex':
      return translate('usage.clientSourceCodex')
    case 'claude':
      return translate('usage.clientSourceClaude')
    case 'pi':
      return translate('usage.clientSourcePi')
    default:
      return translate('usage.clientSourceUnknown')
  }
}

export function getUsageClientSourceBadgeClass(source?: string | null): string {
  switch (normalizeUsageClientSource(source)) {
    case 'codex':
      return 'bg-blue-100 text-blue-700 ring-blue-200 dark:bg-blue-500/20 dark:text-blue-300 dark:ring-blue-500/30'
    case 'claude':
      return 'bg-orange-100 text-orange-700 ring-orange-200 dark:bg-orange-500/20 dark:text-orange-300 dark:ring-orange-500/30'
    case 'pi':
      return 'bg-violet-100 text-violet-700 ring-violet-200 dark:bg-violet-500/20 dark:text-violet-300 dark:ring-violet-500/30'
    default:
      return 'bg-gray-100 text-gray-600 ring-gray-200 dark:bg-gray-500/20 dark:text-gray-300 dark:ring-gray-500/30'
  }
}
