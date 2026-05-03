import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { Header } from './components/layout/Header'
import { Footer } from './components/layout/Footer'
import { MobileBottomNav } from './components/layout/MobileBottomNav'
import { FeedPage } from './pages/FeedPage'
import { ToolsPage } from './pages/ToolsPage'
import { SearchPage } from './pages/SearchPage'
import { LoginPage } from './pages/LoginPage'
import { ProfilePage } from './pages/ProfilePage'
import { AdminPage } from './pages/AdminPage'

export function App() {
  return (
    <BrowserRouter>
      <div className="min-h-screen flex flex-col bg-gray-50 dark:bg-gray-950 text-gray-900 dark:text-gray-100 pb-14 md:pb-0">
        <Header />
        <main className="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 py-6">
          <Routes>
            <Route path="/" element={<FeedPage />} />
            <Route path="/tools" element={<ToolsPage />} />
            <Route path="/search" element={<SearchPage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/profile" element={<ProfilePage />} />
            <Route path="/admin" element={<AdminPage />} />
          </Routes>
        </main>
        <Footer />
        <MobileBottomNav />
      </div>
    </BrowserRouter>
  )
}
