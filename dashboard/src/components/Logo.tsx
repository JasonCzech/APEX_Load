interface LogoProps {
  /** Rendered width/height of the square mark, in px. */
  size?: number
  className?: string
  /** Decorative by default; pass a title to expose an accessible name. */
  title?: string
}

/**
 * APEX Load brand mark — layered summit/apex peaks.
 *
 * The mark is an inline SVG so it inherits the active dashboard theme: every
 * fill is driven by the `--accent*` CSS custom properties, which are redefined
 * per theme in index.css.
 * That makes the logo adapt automatically to light / dark / solarized without
 * shipping separate raster assets.
 */
export default function Logo({ size = 28, className, title }: LogoProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="none"
      className={className}
      role={title ? 'img' : undefined}
      aria-hidden={title ? undefined : true}
      aria-label={title}
    >
      {title ? <title>{title}</title> : null}
      {/* Back peak — recedes via reduced opacity for depth. */}
      <path
        d="M21 9 L30 27 H12 Z"
        fill="var(--accent)"
        opacity="0.4"
      />
      {/* Front peak — the dominant summit. */}
      <path
        d="M12 4 L23 27 H1 Z"
        fill="var(--accent)"
      />
      {/* Snow cap / apex highlight, tinted to read on any background. */}
      <path
        d="M12 4 L16.4 13.2 L12 11.4 L7.6 13.2 Z"
        fill="var(--accent-contrast)"
        opacity="0.85"
      />
      {/* Baseline rule, echoing the wordmark underline. */}
      <rect x="1" y="29" width="30" height="1.6" rx="0.8" fill="var(--accent)" />
    </svg>
  )
}
