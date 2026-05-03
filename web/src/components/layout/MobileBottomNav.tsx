import { NavLink } from 'react-router-dom'

const tabs = [
  {
    to: '/',
    end: true,
    label: 'Feed',
    icon: (
      <path d="M3 12l9-9 9 9M5 10v10a1 1 0 0 0 1 1h3v-6h6v6h3a1 1 0 0 0 1-1V10" />
    ),
  },
  {
    to: '/tools',
    label: 'Tools',
    icon: (
      <path d="M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18l3 3 6.3-6.3a4 4 0 0 0 5.4-5.4l-2.5 2.5-2.5-2.5z" />
    ),
  },
  {
    to: '/search',
    label: 'Search',
    icon: (
      <>
        <circle cx="11" cy="11" r="7" />
        <path d="m21 21-4.3-4.3" />
      </>
    ),
  },
  {
    to: '/profile',
    label: 'Profile',
    icon: (
      <>
        <circle cx="12" cy="8" r="4" />
        <path d="M4 21a8 8 0 0 1 16 0" />
      </>
    ),
  },
  {
    to: '/admin',
    label: 'Admin',
    icon: (
      <path d="M12 2 4 6v6c0 5 3.5 8.5 8 10 4.5-1.5 8-5 8-10V6l-8-4z" />
    ),
  },
]

export function MobileBottomNav() {
  return (
    <nav
      className="fixed inset-x-0 bottom-0 z-40 md:hidden bg-white dark:bg-gray-900 border-t border-gray-200 dark:border-gray-800"
      style={{ paddingBottom: 'env(safe-area-inset-bottom)' }}
    >
      <div className="flex items-stretch">
        {tabs.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            end={tab.end}
            className={({ isActive }) =>
              `flex flex-col items-center justify-center gap-0.5 flex-1 min-h-[56px] text-[11px] font-medium transition-colors ${
                isActive
                  ? 'text-indigo-600 dark:text-indigo-400'
                  : 'text-gray-500 dark:text-gray-400'
              }`
            }
          >
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              {tab.icon}
            </svg>
            <span>{tab.label}</span>
          </NavLink>
        ))}
      </div>
    </nav>
  )
}
