<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Stats } from '../api/types'
import { count, money, moneyCompact } from '../composables/useFormat'

/** Distribution of negotiated rates for the current result set.
 *
 *  Form: a histogram, because the question this answers is "how spread out are
 *  the prices for this code" -- the spread between the cheapest and dearest
 *  negotiated rate for the same procedure is the entire point of price
 *  transparency data. Revenue code 490 runs from $3.76 to $10,955.28.
 *
 *  One series, so no legend: the title names it. The quartile and median lines
 *  are a reference layer drawn in text ink, not a second series -- colour here
 *  carries magnitude, not identity. The series hue is validated against both
 *  the light and dark chart surfaces (see styles.css). */

const props = defineProps<{
  stats: Stats
  /** The rates on the current page. Used only for the small-set view, where
   *  the page necessarily contains every matching rate, so the exact values
   *  can be shown instead of histogram bin edges. */
  pageRates?: number[]
}>()

/** Below this many rates a histogram is the wrong form: there is no
 *  distribution to show, and a single bucket renders as one meaningless
 *  full-width block. The values are listed instead. */
const MIN_FOR_HISTOGRAM = 5

const PAD = { top: 18, right: 14, bottom: 34, left: 14 }
const WIDTH = 760
const HEIGHT = 190
const PLOT_W = WIDTH - PAD.left - PAD.right
const PLOT_H = HEIGHT - PAD.top - PAD.bottom
const GAP = 2 // surface gap between adjacent bars
const RADIUS = 4 // rounded data-end, anchored to the baseline

const hovered = ref<number | null>(null)

const showHistogram = computed(() => props.stats.count >= MIN_FOR_HISTOGRAM)
/** Quartiles are meaningless when every one of them is the same number. */
const showQuartiles = computed(() => props.stats.count >= 4)

const buckets = computed(() => props.stats.histogram ?? [])
const maxCount = computed(() => Math.max(1, ...buckets.value.map((b) => b.count)))
const domain = computed(() => {
  const lo = props.stats.min
  const hi = props.stats.max
  // A single distinct rate would otherwise give a zero-width domain.
  return hi > lo ? { lo, hi } : { lo, hi: lo + 1 }
})

const bars = computed(() =>
  buckets.value.map((b, i) => {
    const n = buckets.value.length
    const slot = PLOT_W / n
    const w = Math.max(1, slot - GAP)
    const h = b.count === 0 ? 0 : Math.max(2, (b.count / maxCount.value) * PLOT_H)
    return {
      index: i,
      bucket: b,
      x: PAD.left + i * slot + GAP / 2,
      y: PAD.top + PLOT_H - h,
      w,
      h,
    }
  }),
)

/** x maps a rate onto the plot, for the reference lines. */
function x(value: number): number {
  const { lo, hi } = domain.value
  const t = Math.min(1, Math.max(0, (value - lo) / (hi - lo)))
  return PAD.left + t * PLOT_W
}

interface Marker {
  key: string
  label: string
  value: number
  emphasis: boolean
}

const markers = computed<Marker[]>(() => {
  const s = props.stats
  if (s.count < 4) return []
  const raw: Marker[] = [
    { key: 'p25', label: 'P25', value: s.p25, emphasis: false },
    { key: 'median', label: 'Median', value: s.median, emphasis: true },
    { key: 'p75', label: 'P75', value: s.p75, emphasis: false },
  ]
  // Drop a label that would collide with its neighbour rather than overprint.
  const out: Marker[] = []
  let lastX = -Infinity
  for (const m of raw) {
    const px = x(m.value)
    if (px - lastX < 48 && !m.emphasis) continue
    out.push(m)
    lastX = px
  }
  return out
})

/** Distinct rates in the small-set view, with how many rows carry each. */
const smallSetValues = computed(() => {
  const source = props.pageRates?.length ? props.pageRates : buckets.value.filter((b) => b.count > 0).map((b) => b.from)
  const tally = new Map<number, number>()
  for (const r of source) tally.set(r, (tally.get(r) ?? 0) + 1)
  return [...tally.entries()]
    .map(([rate, count]) => ({ rate, count }))
    .sort((a, b) => a.rate - b.rate)
})

const hoveredBar = computed(() =>
  hovered.value === null ? null : (bars.value[hovered.value] ?? null),
)

const tooltipX = computed(() => {
  const bar = hoveredBar.value
  if (!bar) return 0
  // Keep the tooltip inside the plot.
  return Math.min(Math.max(bar.x + bar.w / 2, PAD.left + 70), WIDTH - PAD.right - 70)
})
</script>

<template>
  <figure v-if="stats.count > 0" class="card chart">
    <figcaption class="chart-head">
      <div>
        <h3 class="chart-title">
          {{ showHistogram ? 'Negotiated rate distribution' : 'Negotiated rates' }}
        </h3>
        <p class="chart-sub muted">
          {{ count(stats.count) }} rate{{ stats.count === 1 ? '' : 's' }}
          <template v-if="stats.min !== stats.max"> · {{ money(stats.min) }} to {{ money(stats.max) }}</template>
        </p>
      </div>
      <dl v-if="showQuartiles" class="quartiles">
        <div><dt>P25</dt><dd class="nums">{{ money(stats.p25) }}</dd></div>
        <div class="is-median"><dt>Median</dt><dd class="nums">{{ money(stats.median) }}</dd></div>
        <div><dt>P75</dt><dd class="nums">{{ money(stats.p75) }}</dd></div>
        <div><dt>Mean</dt><dd class="nums">{{ money(stats.mean) }}</dd></div>
      </dl>
    </figcaption>

    <!-- Too few values to have a distribution: show the numbers themselves. -->
    <ul v-if="!showHistogram" class="value-list">
      <li v-for="v in smallSetValues" :key="v.rate" class="value">
        <span class="value-amount nums">{{ money(v.rate) }}</span>
        <span v-if="v.count > 1" class="muted value-count">&times;{{ v.count }}</span>
      </li>
    </ul>

    <div v-else class="plot-wrap">
      <svg
        class="plot"
        :viewBox="`0 0 ${WIDTH} ${HEIGHT}`"
        preserveAspectRatio="none"
        role="img"
        :aria-label="`Histogram of ${stats.count} negotiated rates from ${money(stats.min)} to ${money(stats.max)}, median ${money(stats.median)}`"
        @mouseleave="hovered = null"
      >
        <!-- Baseline, recessive. -->
        <line
          :x1="PAD.left" :x2="WIDTH - PAD.right"
          :y1="PAD.top + PLOT_H" :y2="PAD.top + PLOT_H"
          class="axis"
        />

        <!-- Interquartile band, behind the bars. -->
        <rect
          v-if="markers.length"
          :x="x(stats.p25)"
          :y="PAD.top"
          :width="Math.max(0, x(stats.p75) - x(stats.p25))"
          :height="PLOT_H"
          class="iqr"
        />

        <!-- Bars. -->
        <g>
          <rect
            v-for="bar in bars"
            :key="bar.index"
            :x="bar.x"
            :y="bar.y"
            :width="bar.w"
            :height="bar.h"
            :rx="Math.min(RADIUS, bar.w / 2)"
            class="bar"
            :class="{ 'is-hovered': hovered === bar.index, 'is-dim': hovered !== null && hovered !== bar.index }"
          />
        </g>

        <!-- Reference lines, in text ink rather than a series colour. -->
        <g v-for="m in markers" :key="m.key">
          <line
            :x1="x(m.value)" :x2="x(m.value)"
            :y1="PAD.top - 4" :y2="PAD.top + PLOT_H"
            class="marker"
            :class="{ 'is-emphasis': m.emphasis }"
          />
          <text :x="x(m.value)" :y="PAD.top - 8" class="marker-label" :class="{ 'is-emphasis': m.emphasis }">
            {{ m.label }}
          </text>
        </g>

        <!-- Axis end labels only; no number on every bar. -->
        <text :x="PAD.left" :y="HEIGHT - 12" class="axis-label" text-anchor="start">
          {{ moneyCompact(stats.min) }}
        </text>
        <text :x="WIDTH - PAD.right" :y="HEIGHT - 12" class="axis-label" text-anchor="end">
          {{ moneyCompact(stats.max) }}
        </text>

        <!-- Hit targets, wider than the bars. -->
        <rect
          v-for="bar in bars"
          :key="`hit-${bar.index}`"
          :x="bar.x - GAP / 2"
          :y="PAD.top"
          :width="bar.w + GAP"
          :height="PLOT_H"
          class="hit"
          @mouseenter="hovered = bar.index"
        />
      </svg>

      <div
        v-if="hoveredBar"
        class="tooltip"
        :style="{ left: `${(tooltipX / WIDTH) * 100}%` }"
        role="tooltip"
      >
        <strong class="nums">{{ count(hoveredBar.bucket.count) }}</strong>
        rate{{ hoveredBar.bucket.count === 1 ? '' : 's' }}
        <span class="muted nums">
          {{ money(hoveredBar.bucket.from) }} – {{ money(hoveredBar.bucket.to) }}
        </span>
      </div>
    </div>
  </figure>
</template>

<style scoped>
.chart {
  padding: 16px 18px 10px;
  margin: 0;
}

.chart-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 18px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}

.chart-title {
  font-size: 14px;
}

.chart-sub {
  margin: 2px 0 0;
  font-size: 12.5px;
  font-variant-numeric: tabular-nums;
}

.quartiles {
  display: flex;
  gap: 18px;
  margin: 0;
}

.quartiles > div {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.quartiles dt {
  font-size: 10.5px;
  font-weight: 600;
  letter-spacing: 0.03em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.quartiles dd {
  margin: 0;
  font-size: 13.5px;
  color: var(--text-secondary);
}

.quartiles .is-median dd {
  color: var(--text-primary);
  font-weight: 600;
}

.value-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 10px 0 8px;
  padding: 0;
  list-style: none;
}

.value {
  display: inline-flex;
  align-items: baseline;
  gap: 5px;
  padding: 6px 12px;
  background: var(--series-1-soft);
  border: 1px solid var(--series-1);
  border-radius: var(--radius-sm);
}

.value-amount {
  font-size: 15px;
  font-weight: 600;
}

.value-count { font-size: 12px; }

.plot-wrap {
  position: relative;
}

.plot {
  display: block;
  width: 100%;
  height: 190px;
  overflow: visible;
}

.axis {
  stroke: var(--border);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}

.iqr {
  fill: var(--series-1);
  opacity: 0.07;
}

.bar {
  fill: var(--series-1);
  transition: opacity 110ms ease;
}

.bar.is-dim { opacity: 0.4; }
.bar.is-hovered { opacity: 1; }

.marker {
  stroke: var(--text-muted);
  stroke-width: 1;
  stroke-dasharray: 3 3;
  vector-effect: non-scaling-stroke;
}

.marker.is-emphasis {
  stroke: var(--text-primary);
  stroke-width: 2;
  stroke-dasharray: none;
}

.marker-label {
  fill: var(--text-muted);
  font-size: 9.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-anchor: middle;
  font-family: var(--font-sans);
}

.marker-label.is-emphasis {
  fill: var(--text-primary);
}

.axis-label {
  fill: var(--text-muted);
  font-size: 10.5px;
  font-family: var(--font-mono);
}

.hit {
  fill: transparent;
  cursor: crosshair;
}

.tooltip {
  position: absolute;
  top: 2px;
  transform: translateX(-50%);
  padding: 6px 10px;
  background: var(--surface-1);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow);
  font-size: 12px;
  white-space: nowrap;
  pointer-events: none;
  z-index: 5;
}

.tooltip .muted {
  margin-left: 4px;
}

@media (max-width: 640px) {
  .quartiles { gap: 14px; }
  .quartiles dd { font-size: 12.5px; }
  .plot { height: 160px; }
}
</style>
