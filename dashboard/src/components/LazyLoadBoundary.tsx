import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
  title?: string
  className?: string
  onRetry?: () => void
}

interface State {
  failed: boolean
}

export default class LazyLoadBoundary extends Component<Props, State> {
  state: State = { failed: false }

  static getDerivedStateFromError(): State {
    return { failed: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Keep the application mounted and leave a useful diagnostic in developer
    // tooling without exposing chunk URLs or stack details in the UI.
    console.error('Lazy dashboard surface failed to load', error, info)
  }

  private retry = () => {
    if (this.props.onRetry) {
      this.props.onRetry()
      this.setState({ failed: false })
      return
    }
    window.location.reload()
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <div className={this.props.className || 'page-loading lazy-load-error'} role="alert">
        <strong>{this.props.title || 'This part of the dashboard could not be loaded.'}</strong>
        <span>The application may have been updated while this page was open.</span>
        <button type="button" className="btn btn-secondary btn-sm" onClick={this.retry}>Reload and retry</button>
      </div>
    )
  }
}
