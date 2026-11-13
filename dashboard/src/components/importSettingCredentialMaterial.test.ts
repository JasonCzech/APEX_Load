import { describe, expect, it } from 'vitest'
import { importCredentialFragments, importMaskedParts, importRefusesRestoring } from './importSettingCredentialMaterial'

describe('import setting credential material', () => {
 it('finds userinfo, sig and credential-named query material as the server guard does', () => {
  expect(importCredentialFragments('http://svc:pw@proxy.example.test:8080').size).toBe(1)
  expect(importCredentialFragments('https://TOKEN@host.example.test').size).toBe(1)
  expect(importCredentialFragments('https://acct.blob.example.test/c?sv=2020&sig=abc123').size).toBe(1)
  expect(importCredentialFragments('https://api.example.test/?api_key=abcd1234').size).toBe(1)
  expect(importCredentialFragments('https://alice:{{secret.PROXY_PW}}@proxy:8080').size).toBe(0)
  expect(importCredentialFragments('https://api.example.test/?page=2').size).toBe(0)
  expect(importCredentialFragments('alice@example.test').size).toBe(0)
 })

 it('refuses restoring only material the draft no longer carries', () => {
  expect(importRefusesRestoring('http://svc:pw@proxy:8080', 'http://svc:pw@proxy:9090', false)).toBe(false)
  expect(importRefusesRestoring('http://svc:pw@proxy:8080', 'http://proxy:9090', false)).toBe(true)
  expect(importRefusesRestoring('https://a.test/c?sig=abc123', 'https://b.test', false)).toBe(true)
  expect(importRefusesRestoring('http://alice:{{secret.PROXY_PW}}@proxy:8080', 'http://proxy:8080', false)).toBe(false)
  expect(importRefusesRestoring('alice', '{{secret.PROXY_USER}}', true)).toBe(true)
  expect(importRefusesRestoring('{{secret.PROXY_USER}}', 'bob', true)).toBe(false)
 })

 it('finds the parts the masking projection hides that the detector does not count', () => {
  expect([...importMaskedParts('https://a.example.test/c?token=ab')]).toEqual(['ab'])
  expect([...importMaskedParts('https://a.example.test/c?access%5Ftoken=abcdef123456')]).toEqual(['abcdef123456'])
  expect([...importMaskedParts('https://a.example.test/c?token="abcdef123456"')]).toEqual(['"abcdef123456"'])
  expect([...importMaskedParts('https://a.example.test/c?access_token=abc def&x=1')]).toEqual(['abc def'])
  expect([...importMaskedParts('http://svc:@proxy.example.test:8080')]).toEqual(['svc:'])
  expect([...importMaskedParts('https://a.example.test/c?page=2#top')]).toEqual([])
 })

 it('refuses restoring a masked URL part the draft no longer carries, as the import does', () => {
  // On a URL setting the import masks, a part the detector does not count is
  // still refused once the draft dropped it, and passes while the draft keeps it.
  for (const imported of [
   'https://a.example.test/c?token=ab',
   'https://a.example.test/c?access%5Ftoken=abcdef123456',
   'https://a.example.test/c?token="abcdef123456"',
   'https://a.example.test/c?access_token=abc def',
   'https://svc:@a.example.test/c',
  ]) {
   expect(importRefusesRestoring(imported, 'https://a.example.test/c', false, 'config.base_url')).toBe(true)
   expect(importRefusesRestoring(imported, imported.replace('a.example.test', 'b.example.test'), false, 'config.base_url')).toBe(false)
  }
  expect(importRefusesRestoring('http://svc:@proxy.example.test:8080', 'http://proxy.example.test:9090', false, 'config.http.proxy.url')).toBe(true)
  expect(importRefusesRestoring('http://svc:@proxy.example.test:8080', 'http://svc:@proxy.example.test:9090', false, 'config.http.proxy.url')).toBe(false)
  // Settings the import does not URL-mask keep the detector rule alone.
  expect(importRefusesRestoring('https://a.example.test/c?token=ab', 'https://a.example.test/c', false, 'config.appium.server_url')).toBe(false)
  // A URL with no masked part is restorable.
  expect(importRefusesRestoring('https://a.example.test/c?page=2', 'https://b.example.test/c', false, 'config.base_url')).toBe(false)
 })

 it('requires every masked part a detected fragment does not account for to stay in the draft', () => {
  // The kept password does not excuse the dropped short token: the import
  // refuses, so Reset to imported must be hidden.
  const imported = 'https://user:pass1234@a.example.test/c?token=ab'
  for (const path of ['config.base_url', 'config.http.proxy.url', 'config.browser.proxy.server']) {
   expect(importRefusesRestoring(imported, 'https://user:pass1234@a.example.test/c', false, path)).toBe(true)
   expect(importRefusesRestoring(imported, 'https://other:pass1234@b.example.test/c?token=ab', false, path)).toBe(true)
   expect(importRefusesRestoring(imported, 'https://user:pass1234@b.example.test/c?token=ab', false, path)).toBe(false)
  }
  // Detected material fully kept, with nothing else masked, still passes.
  expect(importRefusesRestoring('https://acct.blob.example.test/c?sv=2020&sig=abc123', 'https://other.blob.example.test/c?sv=2020&sig=abc123', false, 'config.base_url')).toBe(false)
  expect(importRefusesRestoring('http://svc:pw12@proxy.example.test:8080', 'http://other:pw12@proxy.example.test:9090', false, 'config.http.proxy.url')).toBe(false)
  // Off the masked settings the detector rule alone applies.
  expect(importRefusesRestoring(imported, 'https://user:pass1234@a.example.test/c', false, 'config.appium.server_url')).toBe(false)
 })

 it('accepts restoring reference-only URL userinfo on masked settings, as the import does', () => {
  for (const imported of [
   'https://svc:{{secret.X}}@a.example.test/c',
   'https://svc:%7B%7Bsecret.X%7D%7D@a.example.test/c',
   'https://{{secret.TOKEN}}@a.example.test/c',
  ]) {
   expect(importRefusesRestoring(imported, 'https://a.example.test/c', false, 'config.base_url')).toBe(false)
  }
  // Literal, empty or lone literal userinfo, or other dropped masked parts beside a reference, stay refused.
  for (const imported of [
   'https://svc:pw@a.example.test/c',
   'https://svc:@a.example.test/c',
   'https://ghp_Hunter2Plain@a.example.test/c',
   'https://svc:{{secret.X}}@a.example.test/c?token=ab',
  ]) {
   expect(importRefusesRestoring(imported, 'https://a.example.test/c', false, 'config.http.proxy.url')).toBe(true)
  }
 })
})
