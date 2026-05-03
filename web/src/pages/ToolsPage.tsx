type Tool = {
  name: string
  desc: string
  cmd: string
  badge?: string
}

const tools: Tool[] = [
  { name: 'Onboard', desc: 'TUI wizard that generates a .env file for the server.', cmd: 'go run ./cmd/onboard' },
  { name: 'Scaffold', desc: 'Code generator with inline validation.', cmd: 'go run ./cmd/scaffold' },
  { name: 'Loadgen', desc: 'HTTP load generator with a live dashboard.', cmd: 'go run ./cmd/loadgen' },
  { name: 'Admin', desc: 'Deployment inspector with a dynamic Picker pattern.', cmd: 'go run ./cmd/admin', badge: 'admin' },
  { name: 'Migrate', desc: 'Run pending database migrations.', cmd: 'make migrate' },
  { name: 'CI', desc: 'Run the full local CI suite (vet, test, lint, gosec).', cmd: 'make ci' },
]

export function ToolsPage() {
  return (
    <div>
      <h1 className="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">Tools</h1>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">CLI tools and developer utilities.</p>

      <div className="mt-6 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
        {tools.map((t) => (
          <div
            key={t.name}
            className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 hover:border-indigo-300 dark:hover:border-indigo-700 hover:shadow-sm transition-all cursor-pointer"
          >
            <div className="flex items-center justify-between">
              <h3 className="font-semibold text-gray-900 dark:text-white">{t.name}</h3>
              {t.badge && (
                <span className="text-[10px] uppercase tracking-wider rounded-full bg-amber-100 dark:bg-amber-950 text-amber-800 dark:text-amber-300 px-2 py-0.5 font-medium">
                  {t.badge}
                </span>
              )}
            </div>
            <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">{t.desc}</p>
            <code className="mt-3 block font-mono text-xs bg-gray-100 dark:bg-gray-800 text-gray-800 dark:text-gray-200 rounded px-2 py-1 truncate">
              {t.cmd}
            </code>
          </div>
        ))}
      </div>
    </div>
  )
}
