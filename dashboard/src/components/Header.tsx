import './Header.css'

interface Props {
  connected: boolean
  onNavigate: () => void
}

export default function Header({ connected, onNavigate }: Props) {
  return (
    <header className="header">
      <div className="header-left" onClick={onNavigate}>
        <img src="/apex-load-logo.png" alt="APEX Load" width="28" height="28" className="header-logo" />
        <h1 className="header-title">APEX Load</h1>
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
