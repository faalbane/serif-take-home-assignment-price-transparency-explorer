import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils'
import ProviderIdInput from './ProviderIdInput.vue'
import { api } from '../api/client'

enableAutoUnmount(afterEach)

beforeEach(() => {
  vi.restoreAllMocks()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.runOnlyPendingTimers()
  vi.useRealTimers()
})

const mountWith = (kind: 'npi' | 'ein', modelValue = '') =>
  mount(ProviderIdInput, { props: { kind, modelValue } })

async function settle() {
  await vi.advanceTimersByTimeAsync(400)
  await flushPromises()
}

/** The warn class is shared by structural warnings and by "not found", so
 *  validation assertions look at the text rather than the class. */
const structuralWarning = (text: string) => /digits|check-digit/.test(text)

describe('NPI validation', () => {
  it('accepts a valid NPI without complaint', async () => {
    vi.spyOn(api, 'providers').mockResolvedValue({ results: [] })
    const w = mountWith('npi')
    await w.find('input').setValue('1396747861')
    await settle()
    expect(structuralWarning(w.text())).toBe(false)
  })

  it('warns, but does not block, an NPI that fails the check digit', async () => {
    // Filed NPIs are sometimes invalid. A tool that refuses to look them up
    // cannot be used to investigate the data-quality problem it surfaced.
    vi.spyOn(api, 'providers').mockResolvedValue({ results: [] })
    const w = mountWith('npi')
    await w.find('input').setValue('1396747862')
    await settle()
    expect(w.find('.hint.warn').text()).toContain('check-digit')
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['1396747862'])
  })

  it('warns about the wrong number of digits', async () => {
    const w = mountWith('npi')
    await w.find('input').setValue('124507857')
    await settle()
    expect(w.find('.hint.warn').text()).toContain('10 digits')
  })

  it('ignores formatting characters when counting digits', async () => {
    vi.spyOn(api, 'providers').mockResolvedValue({ results: [] })
    const w = mountWith('npi')
    await w.find('input').setValue('1396-747-861')
    await settle()
    expect(structuralWarning(w.text())).toBe(false)
  })
})

describe('EIN validation', () => {
  it('accepts the dashed and bare spellings alike', async () => {
    vi.spyOn(api, 'providers').mockResolvedValue({ results: [] })
    for (const value of ['11-3471596', '113471596']) {
      const w = mountWith('ein')
      await w.find('input').setValue(value)
      await settle()
      expect(structuralWarning(w.text())).toBe(false)
    }
  })

  it('warns about the wrong length', async () => {
    const w = mountWith('ein')
    await w.find('input').setValue('1134715')
    await settle()
    expect(w.find('.hint.warn').text()).toContain('9 digits')
  })
})

describe('resolution', () => {
  it('names the organization once the identifier is complete', async () => {
    // Without this, a typo is indistinguishable from "this provider does not
    // price this code": both return nothing.
    vi.spyOn(api, 'providers').mockResolvedValue({
      results: [{
        provider_group_id: 1, business_name: 'Medicine Department of Mount Sinai',
        npi_count: 750, rows: 24,
      } as never],
    })
    const w = mountWith('npi')
    await w.find('input').setValue('1003076902')
    await settle()
    expect(w.text()).toContain('Medicine Department of Mount Sinai')
    expect(w.text()).toContain('24 rates')
  })

  it('says so when the identifier is absent from the data', async () => {
    vi.spyOn(api, 'providers').mockResolvedValue({ results: [] })
    const w = mountWith('npi')
    await w.find('input').setValue('1396747861')
    await settle()
    expect(w.text()).toContain('Not found')
  })

  it('does not look up a partial identifier', async () => {
    const spy = vi.spyOn(api, 'providers').mockResolvedValue({ results: [] })
    const w = mountWith('npi')
    await w.find('input').setValue('13967')
    await settle()
    expect(spy).not.toHaveBeenCalled()
  })

  it('keeps working when resolution fails', async () => {
    vi.spyOn(api, 'providers').mockRejectedValue(new Error('offline'))
    const w = mountWith('npi')
    await w.find('input').setValue('1396747861')
    await settle()
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['1396747861'])
  })
})

describe('clearing', () => {
  it('drops the resolved name immediately rather than after the debounce', async () => {
    vi.spyOn(api, 'providers').mockResolvedValue({
      results: [{ provider_group_id: 1, business_name: 'Baptist Health', npi_count: 1, rows: 9 } as never],
    })
    const w = mountWith('npi')
    await w.find('input').setValue('1396747861')
    await settle()
    expect(w.text()).toContain('Baptist Health')

    await w.find('.clear').trigger('click')
    expect(w.text()).not.toContain('Baptist Health')
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([''])
    expect(w.emitted('submit')).toBeTruthy()
  })

  it('offers no clear button when the field is empty', () => {
    expect(mountWith('npi').find('.clear').exists()).toBe(false)
  })
})
