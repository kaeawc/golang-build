import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { App } from './App'

const originalFetch = globalThis.fetch

beforeEach(() => {
  mock.restore()
})

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
})

describe('App', () => {
  test('renders heading', () => {
    globalThis.fetch = mock(() => Promise.resolve(new Response('[]'))) as unknown as typeof fetch
    render(<App />)
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('golang-build')
  })

  test('renders users from /api/users', async () => {
    globalThis.fetch = mock(() =>
      Promise.resolve(new Response(JSON.stringify([{ id: 1, name: 'Ada' }]))),
    ) as unknown as typeof fetch
    render(<App />)
    await waitFor(() => expect(screen.getByText('Ada')).toBeDefined())
  })

  test('renders error on fetch failure', async () => {
    globalThis.fetch = mock(() => Promise.resolve(new Response('', { status: 500 }))) as unknown as typeof fetch
    render(<App />)
    await waitFor(() => expect(screen.getByText(/Error: HTTP 500/)).toBeDefined())
  })
})
