export function Footer() {
  return (
    <footer className="hidden md:block border-t border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
      <div className="max-w-7xl mx-auto px-6 py-6 text-xs text-gray-500 dark:text-gray-400 flex items-center justify-between">
        <span>© {new Date().getFullYear()} golang-build</span>
        <span>React + Vite + Tailwind, served by the Go API.</span>
      </div>
    </footer>
  )
}
