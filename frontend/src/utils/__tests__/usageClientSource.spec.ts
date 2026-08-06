import { describe, expect, it } from 'vitest'

import {
  getUsageClientSourceBadgeClass,
  getUsageClientSourceLabel,
  normalizeUsageClientSource,
} from '@/utils/usageClientSource'

const translate = (key: string) => ({
  'usage.clientSourceCodex': 'Codex',
  'usage.clientSourceClaude': 'Claude',
  'usage.clientSourceUnknown': 'Unknown',
})[key] ?? key

describe('usageClientSource utils', () => {
  it('normalizes supported values and falls back to unknown', () => {
    expect(normalizeUsageClientSource('codex')).toBe('codex')
    expect(normalizeUsageClientSource('claude')).toBe('claude')
    expect(normalizeUsageClientSource('other')).toBe('unknown')
    expect(normalizeUsageClientSource()).toBe('unknown')
  })

  it('maps sources to translated labels', () => {
    expect(getUsageClientSourceLabel('codex', translate)).toBe('Codex')
    expect(getUsageClientSourceLabel('claude', translate)).toBe('Claude')
    expect(getUsageClientSourceLabel('unknown', translate)).toBe('Unknown')
  })

  it('uses distinct accessible badge palettes', () => {
    expect(getUsageClientSourceBadgeClass('codex')).toContain('bg-blue-100')
    expect(getUsageClientSourceBadgeClass('claude')).toContain('bg-orange-100')
    expect(getUsageClientSourceBadgeClass('unknown')).toContain('bg-gray-100')
  })
})
