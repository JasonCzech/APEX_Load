import { describe, expect, it } from 'vitest'

import { composeHttpURL, normalizeHttpURLInput } from './httpUrlInput'

describe('httpUrlInput', () => {
  it('splits pasted http and https URLs into scheme plus address', () => {
    expect(normalizeHttpURLInput('https://app.example.com/login')).toEqual({
      scheme: 'https://',
      address: 'app.example.com/login',
    })
    expect(normalizeHttpURLInput(' HTTP://localhost:3000/checkout ', 'https://')).toEqual({
      scheme: 'http://',
      address: 'localhost:3000/checkout',
    })
  })

  it('keeps host-only input under the selected scheme', () => {
    expect(normalizeHttpURLInput('app.example.com/login', 'http://')).toEqual({
      scheme: 'http://',
      address: 'app.example.com/login',
    })
  })

  it('composes valid host-only addresses and rejects empty or non-http schemes', () => {
    expect(composeHttpURL('https://', 'app.example.com/login')).toBe('https://app.example.com/login')
    expect(composeHttpURL('http://', 'localhost:3000')).toBe('http://localhost:3000')
    expect(composeHttpURL('https://', '')).toBeNull()
    expect(composeHttpURL('https://', 'file:///etc/passwd')).toBeNull()
  })
})
