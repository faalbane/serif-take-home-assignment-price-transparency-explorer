import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ResultsTable from './ResultsTable.vue'
import type { RateResult, Stats } from '../api/types'

const stats: Stats = {
  count: 3, min: 38.98, max: 231.3, mean: 76.94, median: 68.13, p25: 51.45, p75: 101.05, histogram: [],
}

function row(over: Partial<RateResult> = {}): RateResult {
  return {
    billing_code: '99213',
    billing_code_type: 'CPT',
    name: 'OFFICE/OUTPATIENT VISIT EST',
    description: 'OFFICE OR OTHER OUTPATIENT VISIT',
    negotiated_rate: 68.13,
    billing_class: 'professional',
    setting: 'outpatient',
    expiration_date: '9999-12-31',
    expires: false,
    provider_group: {
      provider_group_id: 1654,
      business_name: 'Kanhaiyalal Kantu MD',
      tin_type: 'ein',
      ein: '112379136',
      ein_display: '11-2379136',
      npi_count: 1,
      npis: ['1063557726'],
    },
    source: {
      id: 0, structure_index: 0, label: 'fidelis-ex (structure 0)',
      url: 'https://example.test/in-network.json',
      reporting_entity_name: 'Centene Management Company LLC',
      last_updated_on: '2026-09-02', version: '2.0.0', plan_count: 88,
    },
    ...over,
  }
}

const mountWith = (results: RateResult[]) =>
  mount(ResultsTable, { props: { results, stats, sort: 'rate_asc', loading: false } })

describe('rendering', () => {
  it('renders one row per result with its rate and provider', () => {
    const w = mountWith([row(), row({ negotiated_rate: 231.3, provider_group: { ...row().provider_group, business_name: 'Other Clinic' } })])
    expect(w.findAll('tbody tr.rate-row')).toHaveLength(2)
    expect(w.text()).toContain('$68.13')
    expect(w.text()).toContain('Kanhaiyalal Kantu MD')
  })

  it('shows the source label, since every file shares the same description', () => {
    expect(mountWith([row()]).text()).toContain('fidelis-ex (structure 0)')
  })

  it('positions each rate against the median of the whole result set', () => {
    const w = mountWith([row({ negotiated_rate: 38.98 })])
    expect(w.text()).toMatch(/−4\d% vs median/)
  })

  it('labels an NPI count rather than printing a bare number', () => {
    const w = mountWith([row({ provider_group: { ...row().provider_group, npi_count: 19, npis: [] } })])
    expect(w.text()).toContain('19 NPIs')
  })

  it('marks an unresolved provider reference instead of hiding the rate', () => {
    const w = mountWith([row({
      provider_group: { ...row().provider_group, unresolved: true, business_name: 'Unresolved provider reference 424242' },
    })])
    expect(w.text()).toContain('unresolved reference')
  })

  it('exposes the sort direction to assistive technology', () => {
    expect(mountWith([row()]).find('th.col-rate').attributes('aria-sort')).toBe('ascending')
  })
})

describe('expansion', () => {
  it('expands to the full provenance via a real button', async () => {
    const w = mountWith([row()])
    expect(w.find('.detail-row').exists()).toBe(false)

    const btn = w.find('.expand-btn')
    expect(btn.attributes('aria-expanded')).toBe('false')
    await btn.trigger('click')

    expect(w.find('.detail-row').exists()).toBe(true)
    expect(w.find('.expand-btn').attributes('aria-expanded')).toBe('true')
    // Provenance the reviewer should be able to see without reading code.
    expect(w.text()).toContain('Centene Management Company LLC')
    expect(w.text()).toContain('2.0.0')
  })

  it('points aria-controls at the detail row it reveals', async () => {
    const w = mountWith([row()])
    const controls = w.find('.expand-btn').attributes('aria-controls')
    await w.find('.expand-btn').trigger('click')
    expect(w.find('.detail-row td').attributes('id')).toBe(controls)
  })

  it('reads the CMS no-expiration sentinel as "does not expire"', async () => {
    const w = mountWith([row()])
    await w.find('.expand-btn').trigger('click')
    expect(w.text()).toContain('sentinel 9999-12-31')
  })

  it('collapses everything when the results change', async () => {
    // Keying expansion by array index left the same position expanded after
    // paging, showing a different provider's provenance under another rate.
    const w = mountWith([row()])
    await w.find('.expand-btn').trigger('click')
    expect(w.find('.detail-row').exists()).toBe(true)

    await w.setProps({ results: [row({ negotiated_rate: 999 })] })
    expect(w.find('.detail-row').exists()).toBe(false)
  })
})

describe('drill-down filters', () => {
  it('emits an EIN filter without toggling the row', async () => {
    const w = mountWith([row()])
    await w.find('.col-ein .link-btn').trigger('click')
    expect(w.emitted('filterEin')?.[0]).toEqual(['112379136'])
    expect(w.find('.detail-row').exists()).toBe(false)
  })

  it('emits an NPI filter for a single-NPI group', async () => {
    const w = mountWith([row()])
    await w.find('.col-npis .link-btn').trigger('click')
    expect(w.emitted('filterNpi')?.[0]).toEqual(['1063557726'])
  })

  it('highlights the NPI that caused the match', () => {
    const w = mountWith([row({
      provider_group: { ...row().provider_group, matched_npi: '1063557726' },
    })])
    expect(w.find('.matched-npi').exists()).toBe(true)
  })
})

describe('untrusted source URLs', () => {
  it('renders an http URL as a link', async () => {
    const w = mountWith([row()])
    await w.find('.expand-btn').trigger('click')
    expect(w.find('.file-link a').attributes('href')).toBe('https://example.test/in-network.json')
  })

  it('refuses to link a javascript: URL from the payer file', async () => {
    const w = mountWith([row({ source: { ...row().source, url: 'javascript:alert(1)' } })])
    await w.find('.expand-btn').trigger('click')
    expect(w.find('.file-link a').exists()).toBe(false)
    expect(w.find('.file-link').text()).toContain('javascript:alert(1)')
  })

  it('does not throw on a malformed percent escape', async () => {
    const w = mountWith([row({ source: { ...row().source, url: 'https://example.test/100%' } })])
    await expect(w.find('.expand-btn').trigger('click')).resolves.not.toThrow()
    expect(w.find('.detail-row').exists()).toBe(true)
  })
})

describe('sorting', () => {
  it('toggles the rate sort direction', async () => {
    const w = mountWith([row()])
    await w.find('.th-sort').trigger('click')
    expect(w.emitted('sort')?.[0]).toEqual(['rate_desc'])
  })
})
