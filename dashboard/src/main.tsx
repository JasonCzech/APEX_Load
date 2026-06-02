import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { initializeDesktopApiOrigin } from './hooks/apiClient'
import './index.css'

async function bootstrap() {
  await initializeDesktopApiOrigin()
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  )
}

void bootstrap()
