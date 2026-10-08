import { Component, type ErrorInfo, type ReactNode } from 'react'
import { reportClientError } from '../utils/clientErrorReporting'

export default class ArchiveReviewBoundary extends Component<{
  resetKey?: string
  archiveName: string
  retryDisabled?: boolean
  onRetry: () => void
  children: ReactNode
}, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() { return { failed: true } }

  componentDidCatch(error: Error, info: ErrorInfo) {
    reportClientError('render', error, { component_stack: info.componentStack ?? undefined })
  }

  componentDidUpdate(previous: Readonly<{ resetKey?: string }>) {
    if (this.state.failed && previous.resetKey !== this.props.resetKey) this.setState({ failed: false })
  }

  render() {
    if (!this.state.failed) return this.props.children
    return <div className="lr-error" role="alert">
      <strong>Could not display the review for {this.props.archiveName}.</strong>
      <p>Your archive queue is still available. Select another archive or validate this archive again. If the problem persists, keep the original ZIP and report the failure.</p>
      <button type="button" className="btn btn-secondary" disabled={this.props.retryDisabled} onClick={this.props.onRetry}>Validate this archive again</button>
    </div>
  }
}
