import { Link, NavLink, useNavigate } from 'react-router-dom'
import { useState } from 'react'

const navLinkClass = ({ isActive }: { isActive: boolean }) =>
  `px-3 py-2 text-sm font-medium rounded-lg transition-colors ${
    isActive
      ? 'text-indigo-600 dark:text-indigo-400 bg-indigo-50 dark:bg-indigo-950'
      : 'text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white hover:bg-gray-100 dark:hover:bg-gray-800'
  }`

export function Header() {
  const navigate = useNavigate()
  const [q, setQ] = useState('')

  const onSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const params = new URLSearchParams()
    if (q.trim()) params.set('q', q.trim())
    navigate({ pathname: '/search', search: params.toString() })
  }

  return (
    <header className="sticky top-0 z-40 bg-white/80 dark:bg-gray-900/80 backdrop-blur border-b border-gray-200 dark:border-gray-800">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 py-3 flex items-center gap-4">
        <Link to="/" className="hidden md:flex items-center gap-2 flex-shrink-0">
          <span className="inline-flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-600 text-white font-bold">g</span>
          <span className="font-semibold text-gray-900 dark:text-white tracking-tight">golang-build</span>
        </Link>

        <form onSubmit={onSubmit} className="flex-1 max-w-xl">
          <div className="relative">
            <svg className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="7" />
              <path d="m21 21-4.3-4.3" />
            </svg>
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              type="search"
              placeholder="Search…"
              aria-label="Search"
              className="w-full pl-9 pr-3 py-2 text-sm rounded-lg bg-gray-100 dark:bg-gray-800 border border-transparent focus:bg-white dark:focus:bg-gray-900 focus:border-indigo-500 focus:outline-none text-gray-900 dark:text-gray-100 placeholder-gray-500"
            />
          </div>
        </form>

        <nav className="hidden md:flex items-center gap-1 flex-shrink-0">
          <NavLink to="/" end className={navLinkClass}>Feed</NavLink>
          <NavLink to="/tools" className={navLinkClass}>Tools</NavLink>
          <NavLink to="/search" className={navLinkClass}>Search</NavLink>
          <NavLink to="/profile" className={navLinkClass}>Profile</NavLink>
          <Link
            to="/login"
            className="ml-2 px-3 py-2 text-sm font-medium rounded-lg bg-indigo-600 text-white hover:bg-indigo-500 transition-colors"
          >
            Log in
          </Link>
        </nav>
      </div>
    </header>
  )
}
