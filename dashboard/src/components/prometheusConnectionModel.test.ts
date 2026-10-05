import { expect, it } from 'vitest'
import { emptyPrometheusQuery, prometheusParameterValid, prometheusQueryValid } from './prometheusConnectionModel'

it('validates typed literals without accepting arbitrary expression insertion', () => {
  expect(prometheusParameterValid({ type: 'label_value', value: '' })).toBe(true)
  expect(prometheusParameterValid({ type: 'label_value', value: 'checkout"},job=~".*' })).toBe(true)
  expect(prometheusParameterValid({ type: 'label_value', value: '😀'.repeat(65) })).toBe(false)
  for (const value of ['500ms', '2m', '1h30m', '1d']) expect(prometheusParameterValid({ type: 'duration', value })).toBe(true)
  for (const value of ['', '0s', '-1m', '25h', '1m1h', '2m] or up[2m', '2.5m']) expect(prometheusParameterValid({ type: 'duration', value })).toBe(false)
  for (const value of ['1970-01-01T00:00:00Z', '2026-10-03T12:00:00.123456789Z']) expect(prometheusParameterValid({ type: 'timestamp', value })).toBe(true)
  for (const value of ['1969-12-31T23:59:59Z', '2026-02-30T00:00:00Z', '2026-10-03T12:00:00+00:00', '12:00:00']) expect(prometheusParameterValid({ type: 'timestamp', value })).toBe(false)
})

it('requires bounded named bindings to be used and every parameter token to be bound', () => {
  const query = { ...emptyPrometheusQuery(), name: 'rate', unit: 'requests/s', expression: 'rate(requests_total{job={{param.target}}}[{{param.lookback}}])', parameters: { target: { type: 'label_value' as const, value: 'checkout' }, lookback: { type: 'duration' as const, value: '2m' } } }
  expect(prometheusQueryValid(query)).toBe(true)
  expect(prometheusQueryValid({ ...query, parameters: { target: query.parameters.target } })).toBe(false)
  expect(prometheusQueryValid({ ...query, parameters: { ...query.parameters, unused: { type: 'label_value', value: 'x' } } })).toBe(false)
  expect(prometheusQueryValid({ ...query, parameters: { ...query.parameters, _invalid: { type: 'label_value', value: 'x' } } })).toBe(false)
})
