import { describe, expect, it } from 'vitest'
import { ApiError, api, toQuery } from './client'

describe('toQuery', () => {
  it('omits empty values so a shared link stays readable', () => {
    expect(toQuery({ code: '99213', npi: '', ein: '   ' }).toString()).toBe('code=99213')
  })

  it('omits the implicit defaults', () => {
    const qs = toQuery({ code: '1', page: 1, page_size: 50, sort: 'rate_asc' })
    expect(qs.toString()).toBe('code=1')
  })

  it('keeps non-default paging and sort', () => {
    const qs = toQuery({ code: '1', page: 2, page_size: 100, sort: 'rate_desc' })
    expect(qs.get('page')).toBe('2')
    expect(qs.get('page_size')).toBe('100')
    expect(qs.get('sort')).toBe('rate_desc')
  })

  it('encodes values that need it', () => {
    expect(toQuery({ ein: '11-3471596' }).toString()).toBe('ein=11-3471596')
    expect(toQuery({ code: 'a b' }).toString()).toBe('code=a+b')
  })
})

describe('csvUrl', () => {
  it('drops paging, since an export is the whole matched set', () => {
    const url = api.csvUrl({ code: '490', page: 7, page_size: 250, billing_class: 'institutional' })
    expect(url).toContain('code=490')
    expect(url).toContain('billing_class=institutional')
    expect(url).not.toContain('page')
  })
})

describe('ApiError', () => {
  it('promotes the detail when the server returned a bare field name', () => {
    // The server puts the rejected field in `error` and the sentence in
    // `detail`; showing the field as a headline gave users a panel reading
    // "min_rate".
    const e = new ApiError(400, { error: 'min_rate', detail: 'min_rate cannot be greater than max_rate' }, 'fallback')
    expect(e.message).toBe('min_rate cannot be greater than max_rate')
    expect(e.field).toBe('min_rate')
  })

  it('keeps a human message as the message', () => {
    const e = new ApiError(503, { error: 'dataset not ready', detail: 'Parsing rate files.' }, 'fallback')
    expect(e.message).toBe('dataset not ready')
    expect(e.detail).toBe('Parsing rate files.')
    expect(e.field).toBe('')
  })

  it('flags a 503 as not-ready so the UI can wait rather than fail', () => {
    expect(new ApiError(503, {}, 'x').notReady).toBe(true)
    expect(new ApiError(400, {}, 'x').notReady).toBe(false)
  })

  it('carries recovery hints', () => {
    const e = new ApiError(400, { error: 'code', detail: 'required', hints: ['Try /api/codes'] }, 'x')
    expect(e.hints).toEqual(['Try /api/codes'])
  })

  it('falls back when the body carries nothing usable', () => {
    expect(new ApiError(500, {}, 'Request failed').message).toBe('Request failed')
  })
})
