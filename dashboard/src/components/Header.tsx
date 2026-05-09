import './Header.css'

interface Props {
  connected: boolean
  onNavigate: () => void
}

export default function Header({ connected, onNavigate }: Props) {
  return (
    <header className="header">
      <div className="header-left" onClick={onNavigate}>
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" className="header-logo">
          <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" fill="var(--accent)" />
        </svg>
        <h1 className="header-title">Stormrunner</h1>
      </div>
      <div className="header-right">
        <span className={`connection-badge ${connected ? 'connected' : 'disconnected'}`}>
          <span className="connection-dot" />
          {connected ? 'Live' : 'Disconnected'}
        </span>
      </div>
    </header>
  )
}
