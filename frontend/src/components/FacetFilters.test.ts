import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import FacetFilters from './FacetFilters.vue'
import { emptySearchParams, type Facets, type SearchParams } from '../api/types'

const facets = (over: Partial<Facets> = {}): Facets => ({
  billing_class: [],
  setting: [],
  billing_code_type: [],
  negotiated_type: [],
  source: [],
  ...over,
})

const fc = (value: string, count: number, label = value) => ({ value, label, count })

const mountWith = (f: Facets, params: Partial<SearchParams> = {}) =>
  mount(FacetFilters, { props: { facets: f, params: { ...emptySearchParams(), ...params } } })

describe('which dimensions render', () => {
  it('shows a dimension with more than one option', () => {
    const w = mountWith(facets({ billing_class: [fc('institutional', 10), fc('professional', 5)] }))
    expect(w.findAll('.chip')).toHaveLength(2)
  })

  it('hides a single-option dimension as noise when nothing is filtered on it', () => {
    // The whole dataset has one `setting` value, so the control cannot narrow.
    const w = mountWith(facets({ setting: [fc('outpatient', 100)] }))
    expect(w.find('.facets').exists()).toBe(false)
  })

  it('shows a single-option dimension when a filter IS active on it', () => {
    // This is the dead end the facet exists to rescue: filtering code 490 to
    // "professional" returns nothing, and the one remaining option carries
    // every row. Hiding it strands the user.
    const w = mountWith(
      facets({ billing_class: [fc('institutional', 208332)] }),
      { billing_class: 'professional' },
    )
    expect(w.find('.facets').exists()).toBe(true)
    expect(w.text()).toContain('institutional')
    expect(w.text()).toContain('208,332')
  })

  it('renders nothing when there are no facets at all', () => {
    expect(mountWith(facets()).find('.facets').exists()).toBe(false)
  })
})

describe('selection', () => {
  it('emits the value on click', async () => {
    const w = mountWith(facets({ billing_class: [fc('institutional', 10), fc('professional', 5)] }))
    await w.findAll('.chip')[0]!.trigger('click')
    expect(w.emitted('update')?.[0]).toEqual([{ billing_class: 'institutional' }])
  })

  it('clears the filter when the active chip is clicked again', async () => {
    const w = mountWith(
      facets({ billing_class: [fc('institutional', 10), fc('professional', 5)] }),
      { billing_class: 'institutional' },
    )
    await w.findAll('.chip')[0]!.trigger('click')
    expect(w.emitted('update')?.[0]).toEqual([{ billing_class: '' }])
  })

  it('marks the active chip with aria-pressed', () => {
    const w = mountWith(
      facets({ billing_class: [fc('institutional', 10), fc('professional', 5)] }),
      { billing_class: 'professional' },
    )
    const pressed = w.findAll('.chip').map((c) => c.attributes('aria-pressed'))
    expect(pressed).toEqual(['false', 'true'])
  })

  it('sends the numeric source id, not the display label', () => {
    // Both files are described "in-network file" in the index, so the label
    // cannot be the filter value.
    const w = mountWith(
      facets({ source: [fc('0', 1108, 'fidelis-ex (structure 0)'), fc('1', 1154, 'fidelis-es (structure 1)')] }),
    )
    expect(w.text()).toContain('fidelis-ex (structure 0)')
    w.findAll('.chip')[0]!.trigger('click')
    expect(w.emitted('update')?.[0]).toEqual([{ source: '0' }])
  })
})

describe('clear all', () => {
  it('appears only when something is filtered, and clears every dimension', async () => {
    const f = facets({ billing_class: [fc('institutional', 10), fc('professional', 5)] })
    expect(mountWith(f).find('.clear-all').exists()).toBe(false)

    const w = mountWith(f, { billing_class: 'professional', min_rate: '10' })
    const clear = w.find('.clear-all')
    expect(clear.exists()).toBe(true)
    expect(clear.text()).toContain('2 filters')

    await clear.trigger('click')
    const payload = w.emitted('update')?.[0]?.[0] as Record<string, string>
    expect(payload.billing_class).toBe('')
    expect(payload.min_rate).toBe('')
  })
})
