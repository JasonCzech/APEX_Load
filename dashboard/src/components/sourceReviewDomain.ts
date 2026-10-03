import type { ScriptDefinition, ScriptImportMetadata, SourceReview } from '../types/scripts'

// Legacy reviews omit the domain. Any nonempty domain must remain source-only
// until an explicit current-script binding contract is available.
export function sourceReviewIsSourceOnly(review?: SourceReview | null): boolean {
  const domain = review?.target_domain
  return domain != null && domain !== ''
}

const object = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined

// Older native-selected imports explicitly changed provenance to salesforce.
// Generic imports retain their original source even when a preview is present.
export function sourceReviewForImport(review: SourceReview, metadata?: ScriptImportMetadata | null): SourceReview {
  if (review.target_domain !== undefined) return review
  const salesforce = object(metadata?.preserved?.salesforce_review)
  return metadata?.source === 'salesforce' && object(salesforce?.native_preview) ? { ...review, target_domain: 'original_import' } : review
}

export function scriptHasSourceOnlyReview(script?: ScriptDefinition | null): boolean {
  const review = script?.metadata?.import?.preserved?.source_review
  return Boolean(review && typeof review === 'object' && !Array.isArray(review) && sourceReviewIsSourceOnly(sourceReviewForImport(review as SourceReview, script?.metadata?.import)))
}
