/** Formatting helpers.
 *
 *  The API returns statistics at full float precision deliberately -- rounding
 *  belongs here, in the presentation layer, so the API never bakes in a
 *  tie-breaking rule. */

const currency = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

const currencyCompact = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  notation: 'compact',
  maximumFractionDigits: 1,
})

const integer = new Intl.NumberFormat('en-US')

export const money = (n: number | undefined | null): string =>
  n === undefined || n === null || !Number.isFinite(n) ? '—' : currency.format(n)

export const moneyCompact = (n: number): string =>
  !Number.isFinite(n) ? '—' : currencyCompact.format(n)

export const count = (n: number | undefined | null): string =>
  n === undefined || n === null || !Number.isFinite(n) ? '—' : integer.format(n)

export const bytes = (n: number): string => {
  if (!Number.isFinite(n) || n <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = n
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

export const seconds = (n: number): string => (n < 10 ? `${n.toFixed(1)}s` : `${Math.round(n)}s`)

/** formatNPI groups a 10-digit NPI for readability without changing its value. */
export const formatNPI = (npi: string): string =>
  npi.length === 10 ? `${npi.slice(0, 3)} ${npi.slice(3, 6)} ${npi.slice(6)}` : npi

/** titleCase renders the SHOUTED names and descriptions in CMS files as
 *  readable text. Tokens that are codes or acronyms are left alone. */
export function titleCase(input: string | undefined): string {
  if (!input) return ''
  // Mixed-case input is already human-written; don't touch it.
  if (input !== input.toUpperCase()) return input

  const keepUpper = new Set([
    'CPT', 'HCPCS', 'RC', 'NPI', 'EIN', 'TIN', 'MRI', 'CT', 'US', 'IV', 'ER',
    'ICU', 'EKG', 'ECG', 'DNA', 'RNA', 'HIV', 'PT', 'OT', 'BX', 'W', 'W/',
    'HSA', 'LLC', 'PC', 'MD', 'DO', 'PA', 'EA', 'PPO', 'HMO', 'EPO',
  ])
  return input
    .toLowerCase()
    .split(/(\s+|\/|-)/)
    .map((token) => {
      const bare = token.trim()
      if (!bare) return token
      const upper = bare.toUpperCase()
      if (keepUpper.has(upper)) return upper
      // Anything containing a digit is a code, not a word.
      if (/\d/.test(bare)) return upper
      // Capitalize the first letter rather than the first character, or a
      // leading bracket or quote leaves the word lowercase:
      // "(SPLIT THICKNESS)" would render as "(split Thickness)".
      return bare.replace(/[a-z]/, (c) => c.toUpperCase())
    })
    .join('')
}

/** safeUrl returns a URL only when it uses a scheme that is safe to put in an
 *  href, otherwise undefined.
 *
 *  Source file URLs come from the payer's index file, which is third-party
 *  content. Vue does not sanitize `:href`, so a `javascript:` location would
 *  execute in the app's origin when a user clicked the link. `target="_blank"`
 *  does not help — browsers ignore the target for that scheme. */
export function safeUrl(url: string | undefined): string | undefined {
  if (!url) return undefined
  return /^https?:\/\//i.test(url.trim()) ? url : undefined
}

/** prettyUrl renders a URL for display, decoding percent escapes when it can.
 *
 *  decodeURIComponent throws a URIError on a malformed escape such as a bare
 *  "%" — a legal character in a URL — which would blank the whole component
 *  via a Vue render error. The default index URL already contains %20, so this
 *  path is live rather than theoretical. */
export function prettyUrl(url: string | undefined): string {
  if (!url) return ''
  try {
    return decodeURIComponent(url)
  } catch {
    return url
  }
}
