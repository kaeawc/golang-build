import { useEffect, useState } from 'react'

type User = { id: number; name: string }

export function App() {
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
    <main className="mx-auto max-w-2xl px-6 py-12">
      <h1 className="text-3xl font-semibold tracking-tight">golang-build</h1>
      <p className="mt-2 text-gray-600">React + Vite + Tailwind, served by the Go API.</p>

      <section className="mt-8">
        <h2 className="text-lg font-medium">Users</h2>
        {error && <p className="mt-2 text-red-600">Error: {error}</p>}
        {!users && !error && <p className="mt-2 text-gray-500">Loading…</p>}
        {users && (
          <ul className="mt-2 divide-y rounded border border-gray-200 bg-white">
            {users.length === 0 && <li className="p-3 text-gray-500">No users yet.</li>}
            {users.map((u) => (
              <li key={u.id} className="p-3">
                <span className="font-mono text-sm text-gray-500">#{u.id}</span>{' '}
                <span>{u.name}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </main>
  )
}
