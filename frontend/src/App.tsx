import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './hooks/useAuth'
import { AccountProvider } from './contexts/AccountContext'
import { ProtectedRoute } from './components/ProtectedRoute'
import { Layout } from './components/Layout'
import { AuthPage } from './pages/AuthPage'
import { DashboardPage } from './pages/DashboardPage'
import { SignalBuilderPage } from './pages/SignalBuilderPage'
import { BacktestPage } from './pages/BacktestPage'
import { OptimizerPage } from './pages/OptimizerPage'
import { WebhooksPage } from './pages/WebhooksPage'
import { AccountsPage } from './pages/AccountsPage'
import { TerminalPage } from './pages/TerminalPage'
import { SignalsPage } from './pages/SignalsPage'
import { AdminPage }   from './pages/AdminPage'
import { SignalChartPage } from './pages/SignalChartPage'
import { AccountPage } from './pages/AccountPage'
import { BotsPage } from './pages/BotsPage'

export default function App() {
  return (
    <AuthProvider>
      <AccountProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<AuthPage defaultTab="login" />} />
          <Route path="/register" element={<AuthPage defaultTab="register" />} />
          <Route
            path="/*"
            element={
              <ProtectedRoute>
                <Layout>
                  <Routes>
                    <Route path="" element={<DashboardPage />} />
                    <Route path="signals/new" element={<SignalBuilderPage />} />
                    <Route path="signals/:id/edit" element={<SignalBuilderPage />} />
                    <Route path="signals/:id/backtest" element={<BacktestPage />} />
                    <Route path="signals/:id/optimize" element={<OptimizerPage />} />
                    <Route path="terminal" element={<TerminalPage />} />
                    <Route path="signals" element={<SignalsPage />} />
                    <Route path="bots" element={<BotsPage />} />
                    <Route path="webhooks" element={<WebhooksPage />} />
                    <Route path="accounts" element={<AccountsPage />} />
                    <Route path="admin"    element={<AdminPage />} />
                    <Route path="signal-chart" element={<SignalChartPage />} />
                    <Route path="account" element={<AccountPage />} />
                    <Route path="*" element={<Navigate to="/" replace />} />
                  </Routes>
                </Layout>
              </ProtectedRoute>
            }
          />
        </Routes>
      </BrowserRouter>
      </AccountProvider>
    </AuthProvider>
  )
}
