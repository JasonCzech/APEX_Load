import { useCallback, useState } from 'react'

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

  const run = useCallback(
    async (task: () => Promise<T>, options?: RunOptions) => {
      setPending(true)
      setError('')
      if (options?.clearValue) {
        setValue(null)
      }

      try {
        const result = await task()
        setValue(result)
        return result
      } catch (error) {
        const message = getErrorMessage(error, 'Operation failed')
        setError(message)
        throw error instanceof Error ? error : new Error(message)
      } finally {
        setPending(false)
      }
    },
    [],
  )

  const reset = useCallback((nextValue: T | null = initialValue) => {
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