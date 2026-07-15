import { useCallback, useEffect, useRef, useState } from 'react'

type RunOptions = {
  clearValue?: boolean
}

function getErrorMessage(error: unknown, fallback: string) {
  if (error instanceof Error && error.message) {
    return error.message
  }
  return fallback
}

export function useAsyncTask<T>(initialValue: T | null = null) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [value, setValue] = useState<T | null>(initialValue)
  const generationRef = useRef(0)
  const mountedRef = useRef(true)

  useEffect(() => {
    // React Strict Mode replays effect setup/cleanup in development. Restore
    // the mounted flag on every setup so the replay cannot permanently disable
    // task results for the lifetime of the component.
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      generationRef.current += 1
    }
  }, [])

  const run = useCallback(
    async (task: () => Promise<T>, options?: RunOptions) => {
      const generation = ++generationRef.current
      setPending(true)
      setError('')
      if (options?.clearValue) {
        setValue(null)
      }

      try {
        const result = await task()
        if (mountedRef.current && generationRef.current === generation) {
          setValue(result)
        }
        return result
      } catch (error) {
        const message = getErrorMessage(error, 'Operation failed')
        if (mountedRef.current && generationRef.current === generation) {
          setError(message)
        }
        throw error instanceof Error ? error : new Error(message)
      } finally {
        if (mountedRef.current && generationRef.current === generation) {
          setPending(false)
        }
      }
    },
    [],
  )

  const reset = useCallback((nextValue: T | null = initialValue) => {
    generationRef.current += 1
    setPending(false)
    setError('')
    setValue(nextValue)
  }, [initialValue])

  const clearError = useCallback(() => {
    setError('')
  }, [])

  return {
    pending,
    error,
    value,
    run,
    reset,
    clearError,
    setValue,
  }
}
