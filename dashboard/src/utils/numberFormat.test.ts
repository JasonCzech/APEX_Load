import { afterEach, expect, it, vi } from 'vitest'
import { formatFixed, formatPercentValue, formatUpTo } from './numberFormat'

afterEach(() => vi.restoreAllMocks())

it('formats percentages with the same separator as other localized numbers', () => {
  const toLocaleString = Number.prototype.toLocaleString
  vi.spyOn(Number.prototype, 'toLocaleString').mockImplementation(function (this: number, _locales?: Intl.LocalesArgument, options?: Intl.NumberFormatOptions) {
    return toLocaleString.call(this, 'de-DE', options)
  })
  expect(formatPercentValue(10.2)).toBe('10,20%')
  expect(formatFixed(21, 1)).toBe('21,0')
  expect(formatPercentValue(0.004)).toBe('<0,01%')
})

it('keeps en-US output and the below-precision marker', () => {
  expect(formatPercentValue(7.98)).toBe('7.98%')
  expect(formatPercentValue(0)).toBe('0.00%')
  expect(formatPercentValue(0.004)).toBe('<0.01%')
  expect(formatPercentValue(12.345, 1)).toBe('12.3%')
})

it('drops trailing zeros up to the digit limit', () => {
  expect(formatUpTo(12)).toBe('12')
  expect(formatUpTo(12.5)).toBe('12.5')
  expect(formatUpTo(1234.5678)).toBe('1,234.57')
  expect(formatUpTo(0.123, 1)).toBe('0.1')
})
