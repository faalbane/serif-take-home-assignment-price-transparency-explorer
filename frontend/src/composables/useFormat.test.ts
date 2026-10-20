import { describe, expect, it } from 'vitest'
import { bytes, count, formatNPI, money, moneyCompact, prettyUrl, safeUrl, seconds, titleCase } from './useFormat'

describe('money', () => {
  it('formats to two decimals', () => {
    expect(money(69.85)).toBe('$69.85')
    expect(money(0)).toBe('$0.00')
    expect(money(10955.28)).toBe('$10,955.28')
  })

  it('returns a dash rather than NaN for missing or non-finite values', () => {
    expect(money(undefined)).toBe('—')
    expect(money(null)).toBe('—')
    expect(money(Number.NaN)).toBe('—')
    expect(money(Number.POSITIVE_INFINITY)).toBe('—')
  })
})

describe('count', () => {
  it('groups thousands', () => {
    expect(count(581860)).toBe('581,860')
  })

  it('returns a dash for missing and non-finite values', () => {
    expect(count(undefined)).toBe('—')
    expect(count(null)).toBe('—')
    expect(count(Number.NaN)).toBe('—')
  })
})

describe('formatNPI', () => {
  it('groups a 10-digit NPI for readability', () => {
    expect(formatNPI('1396747861')).toBe('139 674 7861')
  })

  it('leaves a malformed NPI untouched rather than mis-grouping it', () => {
    // 124507857 is real data: a nine-digit NPI that lost a leading zero.
    expect(formatNPI('124507857')).toBe('124507857')
    expect(formatNPI('0')).toBe('0')
  })
})

describe('titleCase', () => {
  it('leaves already mixed-case text alone', () => {
    expect(titleCase('Northern Westchester Hospital')).toBe('Northern Westchester Hospital')
  })

  it('capitalizes the first letter, not the first character', () => {
    // A leading bracket used to leave the word lowercase.
    expect(titleCase('SKIN GRAFT (SPLIT THICKNESS)')).toBe('Skin Graft (Split Thickness)')
  })

  it('keeps known clinical acronyms uppercase', () => {
    expect(titleCase('FNA BX W/US GDN EA ADDL')).toContain('BX')
    expect(titleCase('PSYTX W PT 60 MINUTES')).toContain('PT')
  })

  it('keeps tokens containing digits uppercase', () => {
    expect(titleCase('X-RAY EXAM OF FOOT 2 VIEWS')).toContain('2')
  })

  it('handles empty and undefined input', () => {
    expect(titleCase(undefined)).toBe('')
    expect(titleCase('')).toBe('')
  })
})

describe('safeUrl', () => {
  it('passes http and https through', () => {
    expect(safeUrl('https://example.test/a.json')).toBe('https://example.test/a.json')
    expect(safeUrl('http://example.test/a.json')).toBe('http://example.test/a.json')
  })

  it.each([
    'javascript:alert(1)',
    'JaVaScRiPt:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'file:///etc/passwd',
    'vbscript:msgbox(1)',
    '/relative/path',
  ])('rejects %s', (bad) => {
    // Source URLs come from a third-party file and Vue does not sanitize href.
    expect(safeUrl(bad)).toBeUndefined()
  })

  it('rejects empty and undefined', () => {
    expect(safeUrl('')).toBeUndefined()
    expect(safeUrl(undefined)).toBeUndefined()
  })
})

describe('prettyUrl', () => {
  it('decodes percent escapes', () => {
    expect(prettyUrl('https://x.test/Centene%20Corporate/a.json')).toBe('https://x.test/Centene Corporate/a.json')
  })

  it('returns the raw string instead of throwing on a malformed escape', () => {
    // decodeURIComponent throws URIError here, which would blank the component.
    expect(prettyUrl('https://x.test/100%')).toBe('https://x.test/100%')
    expect(prettyUrl('%zz')).toBe('%zz')
  })

  it('handles undefined', () => {
    expect(prettyUrl(undefined)).toBe('')
  })
})

describe('bytes and seconds', () => {
  it('scales byte units', () => {
    expect(bytes(512)).toBe('512 B')
    expect(bytes(89359830)).toBe('85.2 MB')
  })

  it('returns a dash for a non-positive size', () => {
    expect(bytes(0)).toBe('—')
    expect(bytes(-1)).toBe('—')
  })

  it('formats elapsed seconds, dropping the decimal once it stops mattering', () => {
    expect(seconds(6.14)).toBe('6.1s')
    expect(seconds(9.4)).toBe('9.4s')
    expect(seconds(13.8)).toBe('14s')
    expect(seconds(42.6)).toBe('43s')
  })
})

describe('moneyCompact', () => {
  it('abbreviates large amounts for axis labels', () => {
    expect(moneyCompact(10955.28)).toBe('$11K')
    expect(moneyCompact(3.76)).toBe('$3.8')
  })
})
