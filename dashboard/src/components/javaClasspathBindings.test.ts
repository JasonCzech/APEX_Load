import { describe, expect, it } from 'vitest'
import type { JavaClasspathAsset, ScriptDefinition } from '../types/scripts'
import { bindJavaClasspathAssets, javaClasspathReviewKey } from './javaClasspathBindings'

describe('Java classpath review bindings', () => {
  const asset: JavaClasspathAsset = { entry: 'client.jar', storage_ref: 'file:///project/client.jar', sha256: 'a'.repeat(64), size_bytes: 12 }
  const script: ScriptDefinition = {
    name: 'Java draft', version: '1.0.0', protocol: 'java_vuser', actions: [],
    config: { base_url: '', java_vuser: { enabled: false, classpath: ['client.jar'], classpath_assets: [asset] } },
  }
  it('retains saved binding provenance when no pending changes exist', () => {
    expect(bindJavaClasspathAssets(script)).toBe(script)
  })
  it('filters orphan bindings and supports removing every attachment without mutating the original', () => {
    expect(bindJavaClasspathAssets(script, [asset, { ...asset, entry: 'orphan.jar' }]).config?.java_vuser?.classpath_assets).toEqual([asset])
    expect(bindJavaClasspathAssets(script, []).config?.java_vuser?.classpath_assets).toEqual([])
    expect(script.config?.java_vuser?.classpath_assets).toEqual([asset])
  })
  it('leaves non-Java drafts unchanged', () => {
    const http = { ...script, protocol: 'http' }
    expect(bindJavaClasspathAssets(http, [asset])).toBe(http)
  })
  it('separates project, source and script identities without delimiter collisions', () => {
    expect(javaClasspathReviewKey('p', 'a:b', 0)).not.toBe(javaClasspathReviewKey('p:a', 'b', 0))
    expect(javaClasspathReviewKey('p', 'a', 0)).not.toBe(javaClasspathReviewKey('q', 'a', 0))
    expect(javaClasspathReviewKey('p', 'a', 0)).not.toBe(javaClasspathReviewKey('p', 'a', 1))
  })
})
