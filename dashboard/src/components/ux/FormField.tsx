import { cloneElement, isValidElement, type ReactNode, type ReactElement } from 'react'
import './ux.css'

/** Shared label, help and error associations; domain validation stays with the form. */
export default function FormField({ label, htmlFor, hint, error, children, className = '' }: { label: string; htmlFor: string; hint?: string; error?: string; children: ReactNode; className?: string }) {
  const control = isValidElement(children) ? children as ReactElement<{ id?: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }> : null
  const describedBy = [control?.props['aria-describedby'], hint && `${htmlFor}-hint`, error && `${htmlFor}-error`].filter(Boolean).join(' ') || undefined
  return <div className={`ux-form-field ${className}`}><label htmlFor={htmlFor}>{label}</label>
    {control ? cloneElement(control, { id: htmlFor, 'aria-describedby': describedBy, 'aria-invalid': error ? true : control.props['aria-invalid'] }) : children}
    {hint && <p id={`${htmlFor}-hint`} className="setting-desc">{hint}</p>}
    {error && <p id={`${htmlFor}-error`} className="settings-error-inline" role="alert">{error}</p>}
  </div>
}
