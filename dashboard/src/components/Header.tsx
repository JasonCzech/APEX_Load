import Logo from './Logo'
import './Header.css'

interface Props {
  connected: boolean
  onNavigate: () => void
}

export default function Header({ connected, onNavigate }: Props) {
  return (
    <header className="header">
      <div className="header-left" onClick={onNavigate}>
        <Logo size={28} className="header-logo" title="APEX Load" />
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
