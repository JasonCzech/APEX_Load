import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { initializeDesktopApiOrigin } from './hooks/apiClient'
import { ApiProvider } from './hooks/useApi'
import './index.css'
import './components/Modal.css'

async function bootstrap() {
  await initializeDesktopApiOrigin()
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <ApiProvider>
        <App />
      </ApiProvider>
    </React.StrictMode>,
  )
}

void bootstrap()
