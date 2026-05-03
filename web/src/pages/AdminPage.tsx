import { useEffect, useState } from 'react'

type User = { id: number; name: string }
type Probe = { name: string; healthy: boolean; latencyNs: number; err?: string }
type TrafficStat = {
  method: string
  path: string
  count: number
  status2xx: number
  status4xx: number
  status5xx: number
  lastStatus: number
  lastNanos: number
  totalNanos: number
}

type Tab = 'users' | 'health' | 'traffic'

export function AdminPage() {
  const [tab, setTab] = useState<Tab>('users')

  return (
    <div>
      <h1 className="text-3xl font-semibold tracking-tight">Admin</h1>

      <nav className="mt-6 flex gap-1 border-b border-gray-200 dark:border-gray-800">
        {(['users', 'health', 'traffic'] as Tab[]).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={
              'px-4 py-2 text-sm capitalize -mb-px border-b-2 ' +
              (tab === t
                ? 'border-indigo-600 text-indigo-600 dark:text-indigo-400'
                : 'border-transparent text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white')
            }
          >
            {t}
          </button>
        ))}
      </nav>

      <section className="mt-6">
        {tab === 'users' && <UsersPanel />}
        {tab === 'health' && <HealthPanel />}
        {tab === 'traffic' && <TrafficPanel />}
      </section>
    </div>
  )
}

function useFetch<T>(url: string, refreshMs?: number): { data: T | null; error: string | null } {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const ac = new AbortController()
    const load = () => {
      fetch(url, { signal: ac.signal })
        .then((r) => {
          if (!r.ok) throw new Error(`HTTP ${r.status}`)
          return r.json() as Promise<T>
        })
        .then((d) => {
          if (!cancelled) setData(d)
        })
        .catch((e: Error) => {
          if (!cancelled && e.name !== 'AbortError') setError(e.message)
        })
    }
    load()
    const id = refreshMs ? window.setInterval(load, refreshMs) : undefined
    return () => {
      cancelled = true
      ac.abort()
      if (id) window.clearInterval(id)
    }
  }, [url, refreshMs])

  return { data, error }
}

function UsersPanel() {
  const { data, error } = useFetch<User[]>('/api/users')
  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!data) return <p className="text-gray-500">Loading…</p>
  return (
    <ul className="divide-y divide-gray-200 dark:divide-gray-800 rounded border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
      {data.length === 0 && <li className="p-3 text-gray-500">No users yet.</li>}
      {data.map((u) => (
        <li key={u.id} className="p-3">
          <span className="font-mono text-sm text-gray-500">#{u.id}</span> {u.name}
        </li>
      ))}
    </ul>
  )
}

function HealthPanel() {
  const { data, error } = useFetch<Probe[]>('/api/admin/healthchecks', 5000)
  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!data) return <p className="text-gray-500">Loading…</p>
  return (
    <ul className="divide-y divide-gray-200 dark:divide-gray-800 rounded border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
      {data.map((p) => (
        <li key={p.name} className="flex items-center justify-between p-3">
          <span className="flex items-center gap-2">
            <span
              className={
                'inline-block h-2 w-2 rounded-full ' +
                (p.healthy ? 'bg-green-500' : 'bg-red-500')
              }
            />
            <span className="font-medium">{p.name}</span>
            {p.err && <span className="ml-2 text-xs text-red-600">{p.err}</span>}
          </span>
          <span className="font-mono text-xs text-gray-500">
            {(p.latencyNs / 1e6).toFixed(1)} ms
          </span>
        </li>
      ))}
    </ul>
  )
}

function TrafficPanel() {
  const { data, error } = useFetch<TrafficStat[]>('/api/admin/traffic', 2000)
  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!data) return <p className="text-gray-500">Loading…</p>
  if (data.length === 0)
    return <p className="text-gray-500">No traffic recorded yet.</p>

  const sorted = [...data].sort((a, b) => b.count - a.count)
  return (
    <table className="w-full overflow-hidden rounded border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 text-sm">
      <thead className="bg-gray-50 dark:bg-gray-800 text-left text-xs uppercase text-gray-600 dark:text-gray-400">
        <tr>
          <th className="p-2">Route</th>
          <th className="p-2 text-right">Count</th>
          <th className="p-2 text-right">2xx</th>
          <th className="p-2 text-right">4xx</th>
          <th className="p-2 text-right">5xx</th>
          <th className="p-2 text-right">Avg</th>
          <th className="p-2 text-right">Last</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-gray-200 dark:divide-gray-800">
        {sorted.map((s) => {
          const avgMs = s.count > 0 ? s.totalNanos / s.count / 1e6 : 0
          return (
            <tr key={s.method + ' ' + s.path}>
              <td className="p-2 font-mono">
                <span className="text-gray-500">{s.method}</span> {s.path}
              </td>
              <td className="p-2 text-right">{s.count}</td>
              <td className="p-2 text-right text-green-700 dark:text-green-400">{s.status2xx}</td>
              <td className="p-2 text-right text-amber-700 dark:text-amber-400">{s.status4xx}</td>
              <td className="p-2 text-right text-red-700 dark:text-red-400">{s.status5xx}</td>
              <td className="p-2 text-right font-mono text-xs">{avgMs.toFixed(1)} ms</td>
              <td className="p-2 text-right font-mono text-xs">
                {(s.lastNanos / 1e6).toFixed(1)} ms
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}
