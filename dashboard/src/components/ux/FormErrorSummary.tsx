import { type ReactNode } from 'react'
import './ux.css'

export type FormIssue = { message: string; onResolve?: () => void }
export default function FormErrorSummary({ issues, children }: { issues: FormIssue[]; children?: ReactNode }) {
  const visible = issues.filter(issue => issue.message)
  if (!visible.length) return null
  return <section className="ux-form-errors" role="alert" aria-label="Items to resolve"><strong>Resolve these items to continue</strong><ul>{visible.map((issue, index) => <li key={index}>{issue.onResolve ? <button type="button" className="btn btn-link" onClick={issue.onResolve}>{issue.message}</button> : issue.message}</li>)}</ul>{children}</section>
}
