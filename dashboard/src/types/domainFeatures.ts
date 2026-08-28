export interface DomainFeatureCategory {
  id: string
  name: string
  description: string
}

export interface DomainFeatureState {
  id: string
  name: string
  description?: string
  category: string
  parent_id?: string
  configured_enabled: boolean
  effective_enabled: boolean
}

export interface DomainFeaturePolicy {
  domain_id: string
  catalog_version: string
  categories: DomainFeatureCategory[]
  features: DomainFeatureState[]
  disabled_feature_ids: string[]
  revision: string
  updated_at?: string
}
