import { useSearchParams } from 'react-router-dom'
import { useMemo } from 'react'

type Result = { id: number; kind: string; title: string; snippet: string }

const corpus: Result[] = [
  { id: 1, kind: 'doc', title: 'Architecture overview', snippet: 'cmd/server hosts the HTTP entry point with mux router and middleware chain…' },
  { id: 2, kind: 'doc', title: 'TUI framework', snippet: 'internal/tui exports a Phase interface and reusable phases (Picker, Confirm, TextInput…)' },
  { id: 3, kind: 'cli', title: 'cmd/admin', snippet: 'TUI deployment inspector demonstrating dynamic Picker fed by an AsyncTask result.' },
  { id: 4, kind: 'cli', title: 'cmd/loadgen', snippet: 'TUI HTTP load generator with a live-updating dashboard.' },
  { id: 5, kind: 'user', title: 'Ada Lovelace', snippet: 'Last active 2h ago · 14 commits this week.' },
  { id: 6, kind: 'user', title: 'Linus Torvalds', snippet: 'Last active 5h ago · maintains middleware chain.' },
]

export function SearchPage() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''

  const results = useMemo(() => {
    if (!q.trim()) return corpus
    const needle = q.toLowerCase()
    return corpus.filter(
      (r) =>
        r.title.toLowerCase().includes(needle) ||
        r.snippet.toLowerCase().includes(needle) ||
        r.kind.toLowerCase().includes(needle),
    )
  }, [q])

  return (
    <div>
      <h1 className="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">Search</h1>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {q ? <>Showing results for <span className="font-medium text-gray-900 dark:text-white">"{q}"</span></> : 'Type in the header search to filter, or browse all.'}
      </p>

      <div className="mt-4 flex flex-wrap gap-2">
        {['doc', 'cli', 'user'].map((kind) => (
          <button
            key={kind}
            type="button"
            onClick={() => setParams(kind === q ? {} : { q: kind })}
            className={`text-xs px-3 py-1 rounded-full border transition-colors ${
              q === kind
                ? 'border-indigo-500 bg-indigo-50 dark:bg-indigo-950 text-indigo-700 dark:text-indigo-300'
                : 'border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-400 hover:border-gray-300 dark:hover:border-gray-600'
            }`}
          >
            {kind}
          </button>
        ))}
      </div>

      <ul className="mt-6 divide-y divide-gray-200 dark:divide-gray-800 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
        {results.length === 0 && (
          <li className="px-4 py-6 text-sm text-gray-500">No results.</li>
        )}
        {results.map((r) => (
          <li key={r.id} className="px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-800/50 cursor-pointer">
            <div className="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
              <span className="inline-block rounded bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 font-mono">{r.kind}</span>
            </div>
            <h3 className="mt-1 text-sm font-semibold text-gray-900 dark:text-white">{r.title}</h3>
            <p className="text-sm text-gray-600 dark:text-gray-400">{r.snippet}</p>
          </li>
        ))}
      </ul>
    </div>
  )
}
