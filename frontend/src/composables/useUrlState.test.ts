import { describe, expect, it, beforeEach } from 'vitest'
import { emptySearchParams, type SearchParams } from '../api/types'
import { readParamsFromUrl, writeParamsToUrl } from './useUrlState'

/** The search state lives in the URL, so a field that does not round-trip is a
 *  shared link that silently means something different to the recipient. */

beforeEach(() => {
  window.history.replaceState(null, '', '/')
})

describe('readParamsFromUrl', () => {
  it('defaults every field when the query string is empty', () => {
    expect(readParamsFromUrl('')).toEqual(emptySearchParams())
  })

  it('reads every supported parameter', () => {
    const p = readParamsFromUrl(
      '?code=99213&code_type=CPT&npi=1396747861&ein=113471596&billing_class=professional' +
        '&setting=outpatient&plan_id=25303NY005&source=1&min_rate=10&max_rate=99' +
        '&sort=rate_desc&page=3&page_size=100',
    )
    expect(p).toEqual({
      code: '99213',
      code_type: 'CPT',
      npi: '1396747861',
      ein: '113471596',
      billing_class: 'professional',
      setting: 'outpatient',
      plan_id: '25303NY005',
      source: '1',
      min_rate: '10',
      max_rate: '99',
      sort: 'rate_desc',
      page: 3,
      page_size: 100,
    })
  })

  it('falls back to the default sort for an unsupported value', () => {
    expect(readParamsFromUrl('?code=1&sort=drop+table').sort).toBe('rate_asc')
  })

  it.each(['rate_asc', 'rate_desc', 'provider', 'npi_count'])('keeps the valid sort %s', (sort) => {
    expect(readParamsFromUrl(`?code=1&sort=${sort}`).sort).toBe(sort)
  })

  it('ignores non-numeric and non-positive paging rather than producing NaN', () => {
    const junk = readParamsFromUrl('?code=1&page=abc&page_size=-5')
    expect(junk.page).toBe(1)
    expect(junk.page_size).toBe(50)
  })

  it('clamps an oversized page size to the server maximum', () => {
    expect(readParamsFromUrl('?code=1&page_size=99999').page_size).toBe(500)
  })

  it('ignores unknown parameters instead of carrying them', () => {
    const p = readParamsFromUrl('?code=1&totally_unknown=x')
    expect(p).not.toHaveProperty('totally_unknown')
    expect(p.code).toBe('1')
  })
})

describe('writeParamsToUrl', () => {
  const withParams = (patch: Partial<SearchParams>): SearchParams => ({
    ...emptySearchParams(),
    ...patch,
  })

  it('omits empty values so a shared link stays readable', () => {
    writeParamsToUrl(withParams({ code: '99213' }))
    expect(window.location.search).toBe('?code=99213')
  })

  it('omits the implicit defaults for page, page size and sort', () => {
    writeParamsToUrl(withParams({ code: '1', page: 1, page_size: 50, sort: 'rate_asc' }))
    expect(window.location.search).toBe('?code=1')
  })

  it('writes paging and sort once they differ from the defaults', () => {
    writeParamsToUrl(withParams({ code: '1', page: 2, page_size: 100, sort: 'rate_desc' }))
    const qs = new URLSearchParams(window.location.search)
    expect(qs.get('page')).toBe('2')
    expect(qs.get('page_size')).toBe('100')
    expect(qs.get('sort')).toBe('rate_desc')
  })

  it('clears the query string entirely when nothing is set', () => {
    writeParamsToUrl(withParams({ code: '1' }))
    writeParamsToUrl(emptySearchParams())
    expect(window.location.search).toBe('')
  })

  it('pushes history by default and replaces when asked', () => {
    const before = window.history.length
    writeParamsToUrl(withParams({ code: 'a' }))
    writeParamsToUrl(withParams({ code: 'b' }), true)
    expect(window.location.search).toBe('?code=b')
    // The replace must not have added a second entry.
    expect(window.history.length).toBeLessThanOrEqual(before + 1)
  })
})

describe('round trip', () => {
  it('survives write then read for every field', () => {
    const original: SearchParams = {
      code: '0450',
      code_type: 'RC',
      npi: '1003076902',
      ein: '611661781',
      billing_class: 'institutional',
      setting: 'outpatient',
      plan_id: '25303NY017',
      source: '0',
      min_rate: '1.5',
      max_rate: '2500',
      sort: 'provider',
      page: 7,
      page_size: 250,
    }
    writeParamsToUrl(original)
    expect(readParamsFromUrl(window.location.search)).toEqual(original)
  })
})
