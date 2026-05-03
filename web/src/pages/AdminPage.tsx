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

type Tab = { id: 'users' | 'health' | 'traffic'; label: string; desc: string }

const tabs: Tab[] = [
  { id: 'users', label: 'Users', desc: 'Application accounts.' },
  { id: 'health', label: 'Health', desc: 'Live probes against backing services.' },
  { id: 'traffic', label: 'Traffic', desc: 'Per-route counters since process start.' },
]

export function AdminPage() {
  const [active, setActive] = useState<Tab['id']>('users')
  const tab = tabs.find((t) => t.id === active)!

  return (
    <div>
      <h1 className="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">Admin</h1>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{tab.desc}</p>

      <nav className="mt-6 flex gap-1 border-b border-gray-200 dark:border-gray-800">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setActive(t.id)}
            className={
              'px-4 py-2 text-sm font-medium -mb-px border-b-2 transition-colors ' +
              (active === t.id
                ? 'border-indigo-600 text-indigo-600 dark:text-indigo-400'
                : 'border-transparent text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white')
            }
          >
            {t.label}
          </button>
        ))}
      </nav>

      <section className="mt-6">
        {active === 'users' && <UsersPanel />}
        {active === 'health' && <HealthPanel />}
        {active === 'traffic' && <TrafficPanel />}
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

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 overflow-hidden">
      {children}
    </div>
  )
}

function StateLine({ kind, children }: { kind: 'error' | 'loading' | 'empty'; children: React.ReactNode }) {
  const cls =
    kind === 'error'
      ? 'text-red-600 dark:text-red-400'
      : 'text-gray-500 dark:text-gray-400'
  return <p className={`text-sm ${cls}`}>{children}</p>
}

function UsersPanel() {
  const { data, error } = useFetch<User[]>('/api/users')
  if (error) return <StateLine kind="error">Error: {error}</StateLine>
  if (!data) return <StateLine kind="loading">Loading…</StateLine>
  if (data.length === 0) return <StateLine kind="empty">No users yet.</StateLine>
  return (
    <Card>
      <ul className="divide-y divide-gray-200 dark:divide-gray-800">
        {data.map((u) => (
          <li key={u.id} className="flex items-center gap-3 p-4 text-sm text-gray-700 dark:text-gray-300">
            <span className="font-mono text-xs text-gray-500">#{u.id}</span>
            <span className="text-gray-900 dark:text-white">{u.name}</span>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function HealthPanel() {
  const { data, error } = useFetch<Probe[]>('/api/admin/healthchecks', 5000)
  if (error) return <StateLine kind="error">Error: {error}</StateLine>
  if (!data) return <StateLine kind="loading">Loading…</StateLine>
  return (
    <Card>
      <ul className="divide-y divide-gray-200 dark:divide-gray-800">
        {data.map((p) => (
          <li key={p.name} className="flex items-center justify-between p-4">
            <span className="flex items-center gap-3">
              <span
                className={
                  'inline-block h-2 w-2 rounded-full ' +
                  (p.healthy ? 'bg-emerald-500' : 'bg-red-500')
                }
              />
              <span className="font-medium text-gray-900 dark:text-white">{p.name}</span>
              <span
                className={
                  'text-xs ' +
                  (p.healthy
                    ? 'text-emerald-600 dark:text-emerald-400'
                    : 'text-red-600 dark:text-red-400')
                }
              >
                {p.healthy ? 'healthy' : 'unhealthy'}
              </span>
              {p.err && (
                <span className="text-xs text-red-600 dark:text-red-400">{p.err}</span>
              )}
            </span>
            <span className="font-mono text-xs text-gray-500">
              {(p.latencyNs / 1e6).toFixed(1)} ms
            </span>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function TrafficPanel() {
  const { data, error } = useFetch<TrafficStat[]>('/api/admin/traffic', 2000)
  if (error) return <StateLine kind="error">Error: {error}</StateLine>
  if (!data) return <StateLine kind="loading">Loading…</StateLine>
  if (data.length === 0) return <StateLine kind="empty">No traffic recorded yet.</StateLine>

  const sorted = [...data].sort((a, b) => b.count - a.count)
  return (
    <Card>
      <table className="w-full text-sm">
        <thead className="bg-gray-50 dark:bg-gray-800/50 text-left text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">
          <tr>
            <th className="px-4 py-2 font-medium">Route</th>
            <th className="px-4 py-2 font-medium text-right">Count</th>
            <th className="px-4 py-2 font-medium text-right">2xx</th>
            <th className="px-4 py-2 font-medium text-right">4xx</th>
            <th className="px-4 py-2 font-medium text-right">5xx</th>
            <th className="px-4 py-2 font-medium text-right">Avg</th>
            <th className="px-4 py-2 font-medium text-right">Last</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-200 dark:divide-gray-800">
          {sorted.map((s) => {
            const avgMs = s.count > 0 ? s.totalNanos / s.count / 1e6 : 0
            return (
              <tr key={s.method + ' ' + s.path}>
                <td className="px-4 py-2 font-mono text-xs">
                  <span className="inline-flex items-center rounded-full bg-indigo-50 dark:bg-indigo-950 text-indigo-700 dark:text-indigo-300 px-2 py-0.5 mr-2 font-medium">
                    {s.method}
                  </span>
                  <span className="text-gray-900 dark:text-white">{s.path}</span>
                </td>
                <td className="px-4 py-2 text-right text-gray-900 dark:text-white">{s.count}</td>
                <td className="px-4 py-2 text-right text-emerald-600 dark:text-emerald-400">{s.status2xx}</td>
                <td className="px-4 py-2 text-right text-amber-600 dark:text-amber-400">{s.status4xx}</td>
                <td className="px-4 py-2 text-right text-red-600 dark:text-red-400">{s.status5xx}</td>
                <td className="px-4 py-2 text-right font-mono text-xs text-gray-600 dark:text-gray-400">{avgMs.toFixed(1)} ms</td>
                <td className="px-4 py-2 text-right font-mono text-xs text-gray-600 dark:text-gray-400">
                  {(s.lastNanos / 1e6).toFixed(1)} ms
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </Card>
  )
}
