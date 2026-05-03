import { useEffect, useState } from 'react'

type User = { id: number; name: string }

type FeedItem = {
  id: number
  author: string
  time: string
  title: string
  body: string
  tag: string
}

const sampleFeed: FeedItem[] = [
  { id: 1, author: 'Ada Lovelace', time: '2h', tag: 'release', title: 'v1.4.0 shipped', body: 'New TUI scaffolding lands with reusable phases. AsyncTask, LiveView, TextInput.' },
  { id: 2, author: 'Linus Torvalds', time: '5h', tag: 'incident', title: 'API latency normalized', body: 'p99 back under 80ms after the connection-pool tuning rolled out.' },
  { id: 3, author: 'Grace Hopper', time: '1d', tag: 'design', title: 'New tools page', body: 'Centralized place for one-off scripts and migrations.' },
]

export function FeedPage() {
  const [users, setUsers] = useState<User[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    fetch('/api/users', { signal: ac.signal })
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return r.json() as Promise<User[]>
      })
      .then(setUsers)
      .catch((e: Error) => {
        if (e.name !== 'AbortError') setError(e.message)
      })
    return () => ac.abort()
  }, [])

  return (
    <div className="grid grid-cols-1 lg:grid-cols-[1fr_280px] gap-6">
      <section>
        <h1 className="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">Feed</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">Recent activity across the system.</p>

        <ul className="mt-6 space-y-3">
          {sampleFeed.map((item) => (
            <li
              key={item.id}
              className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 hover:border-indigo-300 dark:hover:border-indigo-700 transition-colors"
            >
              <div className="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                <span className="inline-flex items-center rounded-full bg-indigo-50 dark:bg-indigo-950 text-indigo-700 dark:text-indigo-300 px-2 py-0.5 font-medium">
                  {item.tag}
                </span>
                <span className="font-medium text-gray-700 dark:text-gray-300">{item.author}</span>
                <span aria-hidden>·</span>
                <span>{item.time}</span>
              </div>
              <h3 className="mt-2 font-semibold text-gray-900 dark:text-white">{item.title}</h3>
              <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">{item.body}</p>
            </li>
          ))}
        </ul>
      </section>

      <aside className="space-y-4">
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4">
          <h2 className="text-sm font-semibold text-gray-900 dark:text-white">Users</h2>
          <p className="text-xs text-gray-500 dark:text-gray-400">From <code className="font-mono">/api/users</code></p>
          <div className="mt-3">
            {error && <p className="text-sm text-red-600">Error: {error}</p>}
            {!users && !error && <p className="text-sm text-gray-500">Loading…</p>}
            {users && users.length === 0 && <p className="text-sm text-gray-500">No users yet.</p>}
            {users && users.length > 0 && (
              <ul className="space-y-1">
                {users.map((u) => (
                  <li key={u.id} className="text-sm text-gray-700 dark:text-gray-300">
                    <span className="font-mono text-xs text-gray-500">#{u.id}</span> {u.name}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>

        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4">
          <h2 className="text-sm font-semibold text-gray-900 dark:text-white">System</h2>
          <ul className="mt-2 space-y-1 text-sm text-gray-600 dark:text-gray-400">
            <li className="flex justify-between"><span>API</span><span className="text-emerald-600 dark:text-emerald-400">healthy</span></li>
            <li className="flex justify-between"><span>Postgres</span><span className="text-emerald-600 dark:text-emerald-400">healthy</span></li>
            <li className="flex justify-between"><span>Valkey</span><span className="text-emerald-600 dark:text-emerald-400">healthy</span></li>
          </ul>
        </div>
      </aside>
    </div>
  )
}
