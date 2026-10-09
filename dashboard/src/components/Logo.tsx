import { useId } from 'react'

interface LogoProps {
  /** Rendered width/height of the square mark, in px. */
  size?: number
  className?: string
  /** Decorative by default; pass a title to expose an accessible name. */
  title?: string
}

/**
 * Theme-driven colors. In the AT&T Light theme these resolve to the brand
 * mark (cobalt tile, white letters, AT&T blue wave); other themes retone it.
 */
const TILE = 'var(--accent)'
const LETTERS = 'var(--accent-contrast)'
const WAVE = 'var(--logo-wave, var(--brand-highlight))'

/** "AL" ligature: the A's right side drops straight down into the L's foot. */
const AL_LIGATURE = '12,84 62,14 62,72 88,72 88,84 50,84 50,53.2 28,84'

/**
 * Rising wave in two cuts: the master for large renders, and a flatter,
 * bolder small cut so the line still reads at sidebar/favicon sizes.
 */
const MASTER_WAVE =
  'M8 66C8.9 66.3 11.4 67.7 13.1 68.1C14.8 68.5 16.5 68.8 18.2 68.6C20 68.4 21.7 67.7 23.4 66.9C25.1 66 26.8 64.7 28.5 63.5C30.2 62.4 31.9 61 33.6 60C35.3 59.1 37 58.2 38.8 57.8C40.5 57.4 42.2 57.4 43.9 57.5C45.6 57.6 47.3 58.1 49 58.3C50.7 58.6 52.4 59 54.1 58.8C55.8 58.7 57.5 58.4 59.2 57.5C61 56.5 62.7 55.1 64.4 53.4C66.1 51.7 67.8 49.5 69.5 47.3C71.2 45.2 72.9 42.7 74.6 40.5C76.3 38.3 78 36.1 79.8 34.2C81.5 32.3 83.2 30.7 84.9 29C86.6 27.3 89.1 24.8 90 24'
const SMALL_WAVE =
  'M8 66C8.9 66.2 11.4 67 13.1 67.2C14.8 67.5 16.5 67.6 18.2 67.4C20 67.2 21.7 66.7 23.4 66C25.1 65.4 26.8 64.4 28.5 63.5C30.2 62.7 31.9 61.6 33.6 60.9C35.3 60.1 37 59.4 38.8 59C40.5 58.6 42.2 58.4 43.9 58.3C45.6 58.2 47.3 58.4 49 58.3C50.7 58.3 52.4 58.3 54.1 58C55.8 57.7 57.5 57.2 59.2 56.3C61 55.3 62.7 54.1 64.4 52.6C66.1 51.1 67.8 49.2 69.5 47.3C71.2 45.4 72.9 43.3 74.6 41.3C76.3 39.3 78 37.3 79.8 35.4C81.5 33.5 83.2 31.8 84.9 29.9C86.6 28 89.1 25 90 24'

/** Renders at or below this size use the small cut. */
const SMALL_CUT_MAX_PX = 32

/**
 * APEX Load brand mark — the "AL" ligature on an accent tile, crossed by a
 * gently rising wave with a fading shaded area beneath it.
 *
 * Colors come from theme tokens so the mark changes tone with the active theme;
 * the favicon and desktop icon carry the fixed AT&T rendering. Gradient/clip ids
 * come from useId so several logos on one page don't share (or clobber) definitions.
 */
export default function Logo({ size = 28, className, title }: LogoProps) {
  const id = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  const small = size <= SMALL_CUT_MAX_PX
  const wave = small ? SMALL_WAVE : MASTER_WAVE
  const tileClip = `apx-${id}-tile`
  const fade = `apx-${id}-fade`

  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 100 100"
      fill="none"
      className={className}
      role={title ? 'img' : undefined}
      aria-hidden={title ? undefined : true}
      aria-label={title}
      data-logo-cut={small ? 'small' : 'master'}
    >
      {title ? <title>{title}</title> : null}
      <defs>
        <clipPath id={tileClip}>
          <rect width="100" height="100" rx="22" />
        </clipPath>
        <linearGradient id={fade} gradientUnits="userSpaceOnUse" x1="0" y1="24" x2="0" y2="96">
          <stop offset="0" style={{ stopColor: WAVE, stopOpacity: small ? 0.45 : 0.6 }} />
          <stop offset="1" style={{ stopColor: WAVE, stopOpacity: 0 }} />
        </linearGradient>
      </defs>
      <rect width="100" height="100" rx="22" fill={TILE} />
      {/* Shaded area under the wave sits behind the letters so they stay crisp. */}
      <path d={`${wave}L90 100L8 100Z`} fill={`url(#${fade})`} clipPath={`url(#${tileClip})`} />
      <polygon points={AL_LIGATURE} fill={LETTERS} />
      <path
        d={wave}
        stroke={WAVE}
        strokeWidth={small ? 7 : 5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
