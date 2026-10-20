import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils'
import CodeTypeahead from './CodeTypeahead.vue'
import { api } from '../api/client'
import type { CodeSummary } from '../api/types'

const summary = (code: string, type = 'CPT', rows = 10, nameCount = 1): CodeSummary => ({
  billing_code: code,
  billing_code_type: type,
  name: `NAME ${code}`,
  description: `DESCRIPTION ${code}`,
  rows,
  min_rate: 1,
  max_rate: 2,
  name_count: nameCount,
})

const mountWith = () => mount(CodeTypeahead, { props: { modelValue: '', codeType: '' }, attachTo: document.body })

// Unmount between tests. A component left mounted keeps its debounce timer,
// which then fires into the next test's spy and makes call counts nonsense.
enableAutoUnmount(afterEach)

beforeEach(() => {
  vi.restoreAllMocks()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.runOnlyPendingTimers()
  vi.useRealTimers()
})

/** advance past the 180ms input debounce and settle the request. */
async function settle() {
  await vi.advanceTimersByTimeAsync(250)
  await flushPromises()
}

describe('typing', () => {
  it('emits the value on every keystroke without searching rates', async () => {
    vi.spyOn(api, 'codes').mockResolvedValue({ query: '9', results: [] })
    const w = mountWith()
    await w.find('input').setValue('99')
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['99'])
  })

  it('debounces the suggestion lookup, so fast typing is one request', async () => {
    const spy = vi.spyOn(api, 'codes').mockResolvedValue({ query: '', results: [] })
    const w = mountWith()
    const input = w.find('input')
    // Five keystrokes inside the 180ms debounce window.
    for (const v of ['9', '99', '992', '9921', '99213']) {
      await input.setValue(v)
      await vi.advanceTimersByTimeAsync(20)
    }
    await settle()
    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy.mock.calls[0]![0]).toBe('99213')
  })

  it('looks up again once typing pauses', async () => {
    const spy = vi.spyOn(api, 'codes').mockResolvedValue({ query: '', results: [] })
    const w = mountWith()
    const input = w.find('input')

    await input.setValue('99')
    await settle()
    await input.setValue('99213')
    await settle()

    expect(spy).toHaveBeenCalledTimes(2)
    expect(spy.mock.calls.at(-1)![0]).toBe('99213')
  })

  it('keeps working when the suggestion lookup fails', async () => {
    vi.spyOn(api, 'codes').mockRejectedValue(new Error('offline'))
    const w = mountWith()
    await w.find('input').setValue('99213')
    await settle()
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['99213'])
    expect(w.find('.list').exists()).toBe(false)
  })
})

describe('suggestions', () => {
  it('lists results and reports how many names share a code', async () => {
    vi.spyOn(api, 'codes').mockResolvedValue({ query: '490', results: [summary('490', 'RC', 208332, 328)] })
    const w = mountWith()
    await w.find('input').setValue('490')
    await settle()

    expect(w.findAll('.opt')).toHaveLength(1)
    expect(w.text()).toContain('208,332')
    expect(w.text()).toContain('328 names')
  })

  it('emits the code and its code system together when one is chosen', async () => {
    // Codes repeat across systems, so a pick must pin both -- in one event,
    // or the parent issues several racing searches.
    vi.spyOn(api, 'codes').mockResolvedValue({ query: '490', results: [summary('490', 'RC')] })
    const w = mountWith()
    await w.find('input').setValue('490')
    await settle()
    await w.find('.opt').trigger('mousedown')

    expect(w.emitted('pick')?.[0]).toEqual([{ code: '490', codeType: 'RC' }])
  })
})

describe('keyboard', () => {
  async function openWithThree() {
    vi.spyOn(api, 'codes').mockResolvedValue({
      query: '9',
      results: [summary('99213'), summary('99214'), summary('99215')],
    })
    const w = mountWith()
    await w.find('input').setValue('9')
    await settle()
    return w
  }

  it('moves the highlight with the arrow keys and wraps around', async () => {
    const w = await openWithThree()
    const input = w.find('input')

    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(input.attributes('aria-activedescendant')).toBe('code-opt-0')

    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(input.attributes('aria-activedescendant')).toBe('code-opt-1')

    await input.trigger('keydown', { key: 'ArrowUp' })
    await input.trigger('keydown', { key: 'ArrowUp' })
    // Wraps to the last option rather than sticking at the top.
    expect(input.attributes('aria-activedescendant')).toBe('code-opt-2')
  })

  it('picks the highlighted option on Enter', async () => {
    const w = await openWithThree()
    const input = w.find('input')
    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('pick')?.[0]).toEqual([{ code: '99213', codeType: 'CPT' }])
  })

  it('submits on Enter when nothing is highlighted', async () => {
    const w = await openWithThree()
    await w.find('input').trigger('keydown', { key: 'Enter' })
    expect(w.emitted('submit')).toBeTruthy()
    expect(w.emitted('pick')).toBeFalsy()
  })

  it('closes the list on Escape without clearing the input', async () => {
    const w = await openWithThree()
    const input = w.find('input')
    await input.trigger('keydown', { key: 'Escape' })
    expect(w.find('.list').exists()).toBe(false)
    expect((input.element as HTMLInputElement).value).toBe('9')
  })
})
