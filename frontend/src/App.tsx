import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './hooks/useAuth'
import { ProtectedRoute } from './components/ProtectedRoute'
import { Layout } from './components/Layout'
import { LoginPage } from './pages/LoginPage'
import { RegisterPage } from './pages/RegisterPage'
import { DashboardPage } from './pages/DashboardPage'
import { SignalBuilderPage } from './pages/SignalBuilderPage'
import { BacktestPage } from './pages/BacktestPage'
import { OptimizerPage } from './pages/OptimizerPage'
import { WebhooksPage } from './pages/WebhooksPage'

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/register" element={<RegisterPage />} />
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
                    <Route path="webhooks" element={<WebhooksPage />} />
                    <Route path="*" element={<Navigate to="/" replace />} />
                  </Routes>
                </Layout>
              </ProtectedRoute>
            }
          />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
