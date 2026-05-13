import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App'

// Force dark mode permanently — remove any stale light-mode state.
document.documentElement.classList.add('dark')
localStorage.removeItem('theme')

const root = document.getElementById('root')
if (!root) throw new Error('Root element #root not found in index.html')

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>
)
