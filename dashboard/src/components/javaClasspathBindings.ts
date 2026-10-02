import type { JavaClasspathAsset, ScriptDefinition } from '../types/scripts'

// Project identity belongs in the key: uploaded capabilities cannot be reused
// when the same queued archive moves to another project.
export function javaClasspathReviewKey(projectId: string | undefined, itemId: string, scriptIndex: number): string {
  return JSON.stringify([projectId || '', itemId, scriptIndex])
}

export function bindJavaClasspathAssets(script: ScriptDefinition, pendingAssets?: JavaClasspathAsset[]): ScriptDefinition {
  if (script.protocol !== 'java_vuser' || pendingAssets === undefined) return script
  const classpath = script.config?.java_vuser?.classpath ?? []
  const assets = pendingAssets.filter(asset => classpath.includes(asset.entry))
  return {
    ...script,
    config: {
      ...script.config,
      java_vuser: { ...script.config?.java_vuser, classpath_assets: assets },
    },
  }
}
