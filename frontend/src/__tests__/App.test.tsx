import { render, screen } from '@testing-library/react'
import App from '../App'

test('renders without crashing', () => {
  render(<App />)
  // Unauthenticated users are redirected to /login (AuthPage, defaultTab="login")
  expect(screen.getByRole('heading', { name: 'Войти в аккаунт' })).toBeInTheDocument()
})
