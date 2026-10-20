import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PaginationBar from './PaginationBar.vue'
import type { Pagination } from '../api/types'

const pag = (over: Partial<Pagination> = {}): Pagination => {
  const page = over.page ?? 1
  const page_size = over.page_size ?? 50
  const total = over.total ?? 2262
  const total_pages = over.total_pages ?? Math.ceil(total / page_size)
  return {
    page, page_size, total, total_pages,
    has_prev: page > 1,
    has_next: page < total_pages,
    ...over,
  }
}

const mountWith = (p: Pagination, loading = false) =>
  mount(PaginationBar, { props: { pagination: p, loading } })

describe('range', () => {
  it('reports the slice currently shown', () => {
    expect(mountWith(pag({ page: 2 })).text()).toContain('51–100 of 2,262')
  })

  it('stops the upper bound at the total on the last page', () => {
    expect(mountWith(pag({ page: 46, total: 2262 })).text()).toContain('2,251–2,262')
  })

  it('renders nothing when there are no results', () => {
    expect(mountWith(pag({ total: 0, total_pages: 0 })).find('.pager').exists()).toBe(false)
  })
})

describe('page buttons', () => {
  it('lists every page when there are few', () => {
    const w = mountWith(pag({ total: 150, page_size: 50 }))
    const labels = w.findAll('.page-btn').map((b) => b.text())
    expect(labels).toEqual(['1', '2', '3'])
  })

  it('windows the list rather than rendering hundreds of buttons', () => {
    // Revenue code 490 is 417 pages at the default size.
    const w = mountWith(pag({ total: 208332, page_size: 500, page: 200 }))
    const labels = w.findAll('.page-btn').map((b) => b.text())
    expect(labels.length).toBeLessThanOrEqual(7)
    expect(labels[0]).toBe('1')
    expect(labels.at(-1)).toBe('417')
    expect(labels).toContain('200')
    expect(w.findAll('.gap').length).toBeGreaterThan(0)
  })

  it('marks the current page for assistive technology', () => {
    const w = mountWith(pag({ page: 3 }))
    const current = w.findAll('.page-btn').find((b) => b.text() === '3')
    expect(current?.attributes('aria-current')).toBe('page')
  })

  it('emits the requested page', async () => {
    const w = mountWith(pag({ page: 1 }))
    const two = w.findAll('.page-btn').find((b) => b.text() === '2')
    await two!.trigger('click')
    expect(w.emitted('page')?.[0]).toEqual([2])
  })
})

describe('prev and next', () => {
  it('disables prev on the first page and next on the last', () => {
    const first = mountWith(pag({ page: 1 }))
    expect(first.findAll('.btn')[0]!.attributes('disabled')).toBeDefined()

    const last = mountWith(pag({ page: 46, total: 2262 }))
    const next = last.findAll('.btn').find((b) => b.text().includes('Next'))
    expect(next!.attributes('disabled')).toBeDefined()
  })

  it('disables every control while a request is in flight', () => {
    const w = mountWith(pag({ page: 2 }), true)
    expect(w.findAll('button').every((b) => b.attributes('disabled') !== undefined)).toBe(true)
  })
})

describe('page size', () => {
  it('emits the chosen size', async () => {
    const w = mountWith(pag())
    await w.find('select').setValue('100')
    expect(w.emitted('pageSize')?.[0]).toEqual([100])
  })
})
