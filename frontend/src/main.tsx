import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './app/App'
import { wailsBackend } from './lib/wailsBackend'
import { markPlatform } from './lib/platform'
import { applyTheme, readTheme } from './state/theme'
import './styles/global.css'

markPlatform()
applyTheme(readTheme(), false)

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App backend={wailsBackend} />
  </React.StrictMode>,
)
