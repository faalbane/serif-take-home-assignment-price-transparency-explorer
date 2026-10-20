import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import RateDistribution from './RateDistribution.vue'
import type { Bucket, Stats } from '../api/types'

function histogram(counts: number[], lo = 0, hi = 100): Bucket[] {
  const width = (hi - lo) / counts.length
  return counts.map((count, i) => ({ from: lo + i * width, to: lo + (i + 1) * width, count }))
}

const stats = (over: Partial<Stats> = {}): Stats => ({
  count: 2262, min: 38.98, max: 231.3, mean: 76.94, median: 68.13, p25: 51.45, p75: 101.05,
  histogram: histogram([5, 40, 120, 60, 20, 8], 38.98, 231.3),
  ...over,
})

const mountWith = (s: Stats, pageRates?: number[]) =>
  mount(RateDistribution, { props: { stats: s, ...(pageRates ? { pageRates } : {}) } })

describe('with enough values to have a distribution', () => {
  it('draws one bar per bucket', () => {
    const w = mountWith(stats())
    expect(w.findAll('.bar')).toHaveLength(6)
  })

  it('shows the quartile summary', () => {
    const w = mountWith(stats())
    expect(w.text()).toContain('$51.45')
    expect(w.text()).toContain('$68.13')
    expect(w.text()).toContain('$101.05')
  })

  it('draws reference lines for the quartiles', () => {
    expect(mountWith(stats()).findAll('.marker').length).toBeGreaterThan(0)
  })

  it('labels the axis with the real extremes', () => {
    const w = mountWith(stats())
    const labels = w.findAll('.axis-label').map((l) => l.text())
    expect(labels[0]).toContain('$39')
    expect(labels.at(-1)).toContain('$231')
  })

  it('carries an accessible summary of the whole distribution', () => {
    const label = mountWith(stats()).find('svg').attributes('aria-label')
    expect(label).toContain('2,262')
    expect(label).toContain('$38.98')
    expect(label).toContain('$231.30')
  })

  it('gives every bar a hit target wider than the bar', () => {
    expect(mountWith(stats()).findAll('.hit')).toHaveLength(6)
  })
})

describe('with too few values to have a distribution', () => {
  it('lists the values instead of drawing a one-bar histogram', () => {
    // A single result previously rendered as a full-width block, which reads
    // as a broken chart rather than as one number.
    const one = stats({
      count: 1, min: 70.16, max: 70.16, mean: 70.16, median: 70.16, p25: 70.16, p75: 70.16,
      histogram: [{ from: 70.16, to: 70.16, count: 1 }],
    })
    const w = mountWith(one, [70.16])

    expect(w.find('.plot').exists()).toBe(false)
    expect(w.findAll('.value')).toHaveLength(1)
    expect(w.text()).toContain('$70.16')
  })

  it('hides quartiles when every one of them is the same number', () => {
    const one = stats({
      count: 1, min: 70.16, max: 70.16, mean: 70.16, median: 70.16, p25: 70.16, p75: 70.16,
      histogram: [{ from: 70.16, to: 70.16, count: 1 }],
    })
    expect(mountWith(one, [70.16]).find('.quartiles').exists()).toBe(false)
  })

  it('shows exact values from the page rather than histogram bin edges', () => {
    const few = stats({
      count: 3, min: 42.03, max: 84.06, mean: 67.65, median: 76.85, p25: 59.44, p75: 80.46,
      histogram: histogram([1, 0, 1, 0, 1], 42.03, 84.06),
    })
    const w = mountWith(few, [76.85, 42.03, 84.06])
    const shown = w.findAll('.value-amount').map((v) => v.text())
    expect(shown).toEqual(['$42.03', '$76.85', '$84.06'])
  })

  it('collapses repeated values with a multiplier', () => {
    const few = stats({
      count: 4, min: 41.31, max: 68.13, mean: 48.0, median: 41.31, p25: 41.31, p75: 48.0,
      histogram: histogram([3, 0, 1], 41.31, 68.13),
    })
    const w = mountWith(few, [41.31, 41.31, 41.31, 68.13])
    expect(w.findAll('.value')).toHaveLength(2)
    expect(w.text()).toContain('×3')
  })
})

describe('empty', () => {
  it('renders nothing at all for a zero-row result', () => {
    const none = stats({ count: 0, min: 0, max: 0, mean: 0, median: 0, p25: 0, p75: 0, histogram: [] })
    expect(mountWith(none).find('figure').exists()).toBe(false)
  })
})
