import { useEffect, useState } from 'react'
export default function ActivityWarning() {
  const [visible, setVisible] = useState(false)
  useEffect(() => { const show = () => setVisible(true); window.addEventListener('apexload:activity-warning', show); return () => window.removeEventListener('apexload:activity-warning', show) }, [])
  if (!visible) return null
  return <div className="main-topbar-feedback" role="status">Change saved; activity history could not be recorded.<button type="button" className="btn btn-ghost" onClick={() => setVisible(false)}>Dismiss activity warning</button></div>
}
