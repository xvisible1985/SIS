import { render, screen } from '@testing-library/react'
import App from '../App'

test('renders without crashing', () => {
  render(<App />)
  // Unauthenticated users are redirected to /login which shows the SIS heading
  expect(screen.getByRole('heading', { name: 'SIS' })).toBeInTheDocument()
})
