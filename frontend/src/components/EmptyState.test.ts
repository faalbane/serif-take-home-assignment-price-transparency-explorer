import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import EmptyState from './EmptyState.vue'
import type { CodeSummary } from '../api/types'

const code = (c: string, rows = 10): CodeSummary => ({
  billing_code: c, billing_code_type: 'CPT', name: `NAME ${c}`, description: `DESC ${c}`,
  rows, min_rate: 1, max_rate: 2, name_count: 1,
})

describe('initial', () => {
  it('invites a search and shows what is searchable', () => {
    const w = mount(EmptyState, { props: { variant: 'initial', topCodes: [code('99213'), code('490')] } })
    expect(w.text()).toContain('Search negotiated rates by billing code')
    expect(w.findAll('.suggest-card')).toHaveLength(2)
  })

  it('emits the chosen code', async () => {
    const w = mount(EmptyState, { props: { variant: 'initial', topCodes: [code('99213')] } })
    await w.find('.suggest-card').trigger('click')
    expect(w.emitted('pick')?.[0]?.[0]).toMatchObject({ billing_code: '99213' })
  })
})

describe('no results', () => {
  it('blames the filter when the code genuinely has rates', () => {
    const w = mount(EmptyState, {
      props: {
        variant: 'no-results', code: '490', codeExists: true,
        activeFilters: [{ label: 'billing class', value: 'professional' }],
      },
    })
    expect(w.text()).toContain('has rates')
    expect(w.text()).toContain('billing class')
  })

  it('does not claim the code exists when nothing says it does', () => {
    // A shared URL for a code that is not in the data used to assert
    // "Code X exists, but...", which is simply false.
    const w = mount(EmptyState, {
      props: {
        variant: 'no-results', code: '99999', codeExists: false,
        activeFilters: [{ label: 'code system', value: 'CPT' }],
      },
    })
    expect(w.text()).not.toContain('has rates')
    expect(w.text()).toContain('does not appear in the ingested files')
  })

  it('says the code was never filed when there are no filters at all', () => {
    const w = mount(EmptyState, { props: { variant: 'no-results', code: 'ZZZZZ', activeFilters: [] } })
    expect(w.text()).toContain('No in-network rate was filed')
  })

  it('offers a way to clear the filters', async () => {
    const w = mount(EmptyState, {
      props: {
        variant: 'no-results', code: '490', codeExists: true,
        activeFilters: [{ label: 'NPI', value: '1003076902' }],
      },
    })
    await w.find('.active-list .btn').trigger('click')
    expect(w.emitted('clearFilters')).toBeTruthy()
  })
})

describe('past end', () => {
  it('explains the page is out of range and offers a way back', async () => {
    const w = mount(EmptyState, {
      props: { variant: 'past-end', code: '490', total: 208332, page: 9999, totalPages: 417 },
    })
    expect(w.text()).toContain('Page 9999 is past the end')
    expect(w.text()).toContain('208,332')
    await w.find('.retry').trigger('click')
    expect(w.emitted('firstPage')).toBeTruthy()
  })
})

describe('error', () => {
  it('shows the message, detail and hints, and offers a retry', async () => {
    const w = mount(EmptyState, {
      props: {
        variant: 'error',
        errorMessage: 'Cannot reach the API',
        errorDetail: 'Connection refused',
        errorHints: ['Is the Go server running?'],
      },
    })
    expect(w.text()).toContain('Cannot reach the API')
    expect(w.text()).toContain('Connection refused')
    expect(w.text()).toContain('Is the Go server running?')
    await w.find('.retry').trigger('click')
    expect(w.emitted('retry')).toBeTruthy()
  })
})
