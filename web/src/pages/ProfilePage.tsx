export function ProfilePage() {
  return (
    <div>
      <h1 className="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">Profile</h1>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">Your account and preferences.</p>

      <div className="mt-6 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 flex items-center gap-4">
        <span className="inline-flex h-14 w-14 items-center justify-center rounded-full bg-indigo-600 text-white text-xl font-semibold">
          A
        </span>
        <div>
          <h2 className="text-lg font-semibold text-gray-900 dark:text-white">Anonymous</h2>
          <p className="text-sm text-gray-500 dark:text-gray-400">guest@golang-build.local</p>
        </div>
      </div>

      <div className="mt-4 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 divide-y divide-gray-200 dark:divide-gray-800">
        {[
          { label: 'Notifications', value: 'On' },
          { label: 'Theme', value: 'System' },
          { label: 'API key', value: '••••••••' },
        ].map((row) => (
          <div key={row.label} className="px-4 py-3 flex items-center justify-between">
            <span className="text-sm text-gray-700 dark:text-gray-300">{row.label}</span>
            <span className="text-sm text-gray-500 dark:text-gray-400">{row.value}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
