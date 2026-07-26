import { describe, expect, it } from 'vitest'
import { formatCompactNumber, formatUsageCost } from '../format'

describe('formatCompactNumber', () => {
  it('formats boundary values with K/M/B', () => {
    expect(formatCompactNumber(0)).toBe('0')
    expect(formatCompactNumber(999)).toBe('999')
    expect(formatCompactNumber(1000)).toBe('1.0K')
    expect(formatCompactNumber(999999)).toBe('1000.0K')
    expect(formatCompactNumber(1000000)).toBe('1.0M')
    expect(formatCompactNumber(1000000000)).toBe('1.0B')
  })

  it('supports disabling billion unit (requests style)', () => {
    expect(formatCompactNumber(1000000000, { allowBillions: false })).toBe('1000.0M')
  })

  it('returns 0 for nullish input', () => {
    expect(formatCompactNumber(null)).toBe('0')
    expect(formatCompactNumber(undefined)).toBe('0')
  })
})

describe('formatUsageCost', () => {
  it('preserves meaningful precision for sub-cent usage', () => {
    expect(formatUsageCost(0)).toBe('$0.00')
    expect(formatUsageCost(0.0037)).toBe('$0.0037')
    expect(formatUsageCost(0.01)).toBe('$0.01')
    expect(formatUsageCost(1.234)).toBe('$1.23')
  })

  it('uses a lower-bound marker for tiny positive usage', () => {
    expect(formatUsageCost(0.00001)).toBe('<$0.0001')
  })
})
