import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react'

import { ApplicationRepository, Domain, Project, Release, RepositoryCategory } from '../types'
import { useRepositoryApi } from '../hooks/api/useRepositoryApi'

interface Props {
  domains: Domain[]
  activeDomain: Domain | null
  applicationRepository: ApplicationRepository | null
  categories: RepositoryCategory[]
  projects: Project[]
  canManageRepository: boolean
  canManageProjects: boolean
  canManageReleases: boolean
  onDomainChange: (domain: Domain) => void
  onRepositoryRefresh: () => Promise<void>
  onProjectsRefresh: () => Promise<void>
  onProjectCreated: (project: { name: string; domain_id?: string; category_id?: string }) => Promise<Project>
}

export default function ApplicationRepositoryPage({
  domains,
  activeDomain,
  applicationRepository,
  categories,
  projects,
  canManageRepository,
  canManageProjects,
  canManageReleases,
  onDomainChange,
  onRepositoryRefresh,
  onProjectsRefresh,
  onProjectCreated,
}: Props) {
  const api = useRepositoryApi()
  const [newCategoryName, setNewCategoryName] = useState('')
  const [newProjectNameByCategory, setNewProjectNameByCategory] = useState<Record<string, string>>({})
  const [newReleaseNameByProject, setNewReleaseNameByProject] = useState<Record<string, string>>({})
  const [releasesByProject, setReleasesByProject] = useState<Record<string, Release[]>>({})
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')

  const scopedProjects = useMemo(() => {
    const domainId = activeDomain?.id || 'default'
    return projects.filter(project => (project.domain_id || 'default') === domainId)
  }, [activeDomain?.id, projects])

  const projectsByCategory = useMemo(() => {
    const grouped: Record<string, Project[]> = {}
    for (const category of categories) {
      grouped[category.id] = []
    }
    for (const project of scopedProjects) {
      const categoryId = project.category_id || categories[0]?.id || 'uncategorized'
      grouped[categoryId] = grouped[categoryId] || []
      grouped[categoryId].push(project)
    }
    Object.values(grouped).forEach(items => items.sort((a, b) => a.name.localeCompare(b.name)))
    return grouped
  }, [categories, scopedProjects])

  const refreshReleases = useCallback(async () => {
    const entries = await Promise.all(scopedProjects.map(async project => {
      try {
        const response = await api.listProjectReleases(project.id)
        return [project.id, response.releases || []] as const
      } catch {
        return [project.id, []] as const
      }
    }))
    setReleasesByProject(Object.fromEntries(entries))
  }, [api, scopedProjects])

  useEffect(() => {
    refreshReleases().catch(() => undefined)
  }, [refreshReleases])

  const submitCategory = async (event: FormEvent) => {
    event.preventDefault()
    if (!activeDomain || !newCategoryName.trim()) return
    setError('')
    setStatus('')
    try {
      await api.createRepositoryCategory(activeDomain.id, { name: newCategoryName.trim(), sort_order: categories.length + 1 })
      setNewCategoryName('')
      await onRepositoryRefresh()
      setStatus('Category created.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to create category.')
    }
  }

  const submitProject = async (event: FormEvent, category: RepositoryCategory) => {
    event.preventDefault()
    const name = (newProjectNameByCategory[category.id] || '').trim()
    if (!activeDomain || !name) return
    setError('')
    setStatus('')
    try {
      await onProjectCreated({ name, domain_id: activeDomain.id, category_id: category.id })
      setNewProjectNameByCategory(prev => ({ ...prev, [category.id]: '' }))
      await onProjectsRefresh()
      setStatus('Project created.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to create project.')
    }
  }

  const submitRelease = async (event: FormEvent, project: Project) => {
    event.preventDefault()
    const name = (newReleaseNameByProject[project.id] || '').trim()
    if (!name) return
    setError('')
    setStatus('')
    try {
      const release = await api.createProjectRelease(project.id, { name })
      setReleasesByProject(prev => ({ ...prev, [project.id]: [release, ...(prev[project.id] || [])] }))
      setNewReleaseNameByProject(prev => ({ ...prev, [project.id]: '' }))
      setStatus('Release created.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to create release.')
    }
  }

  return (
    <div className="page repository-page">
      <div className="page-header">
        <div>
          <p className="page-kicker">Domain</p>
          <h1>Application Repository</h1>
          <p className="page-subtitle">Organize performance work by category, project, and release inside the selected domain.</p>
        </div>
        <label className="repository-domain-select">
          <span>Domain</span>
          <select
            value={activeDomain?.id || ''}
            onChange={event => {
              const next = domains.find(domain => domain.id === event.target.value)
              if (next) onDomainChange(next)
            }}
          >
            {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
          </select>
        </label>
      </div>

      <section className="repository-summary-band">
        <div>
          <span className="metric-label">Repository</span>
          <strong>{applicationRepository?.name || 'Application Repository'}</strong>
        </div>
        <div>
          <span className="metric-label">Categories</span>
          <strong>{categories.length}</strong>
        </div>
        <div>
          <span className="metric-label">Projects</span>
          <strong>{scopedProjects.length}</strong>
        </div>
        <div>
          <span className="metric-label">Access</span>
          <strong>{canManageProjects ? 'Read/write' : 'Read-only'}</strong>
        </div>
      </section>

      {(status || error) && (
        <div className={error ? 'form-error' : 'form-success'}>{error || status}</div>
      )}

      {canManageRepository && activeDomain && (
        <form className="repository-inline-form" onSubmit={submitCategory}>
          <label>
            <span>New Category</span>
            <input value={newCategoryName} onChange={event => setNewCategoryName(event.target.value)} placeholder="Payments, Claims, Search" />
          </label>
          <button type="submit" disabled={!newCategoryName.trim()}>Add Category</button>
        </form>
      )}

      <div className="repository-category-grid">
        {categories.map(category => (
          <section key={category.id} className="repository-category">
            <div className="repository-category-header">
              <div>
                <h2>{category.name}</h2>
                {category.description && <p>{category.description}</p>}
              </div>
              <span>{projectsByCategory[category.id]?.length || 0} projects</span>
            </div>

            {canManageProjects && (
              <form className="repository-compact-form" onSubmit={event => submitProject(event, category)}>
                <input
                  value={newProjectNameByCategory[category.id] || ''}
                  onChange={event => setNewProjectNameByCategory(prev => ({ ...prev, [category.id]: event.target.value }))}
                  placeholder="New project name"
                />
                <button type="submit" disabled={!(newProjectNameByCategory[category.id] || '').trim()}>Create Project</button>
              </form>
            )}

            <div className="repository-project-list">
              {(projectsByCategory[category.id] || []).map(project => (
                <article key={project.id} className="repository-project">
                  <div className="repository-project-title">
                    <div>
                      <h3>{project.name}</h3>
                      <p>{project.id}</p>
                    </div>
                    <span>{(releasesByProject[project.id] || []).length} releases</span>
                  </div>
                  {canManageReleases && (
                    <form className="repository-compact-form" onSubmit={event => submitRelease(event, project)}>
                      <input
                        value={newReleaseNameByProject[project.id] || ''}
                        onChange={event => setNewReleaseNameByProject(prev => ({ ...prev, [project.id]: event.target.value }))}
                        placeholder="New release name"
                      />
                      <button type="submit" disabled={!(newReleaseNameByProject[project.id] || '').trim()}>Add Release</button>
                    </form>
                  )}
                  <div className="repository-release-list">
                    {(releasesByProject[project.id] || []).map(release => (
                      <span key={release.id}>{release.name}</span>
                    ))}
                    {(releasesByProject[project.id] || []).length === 0 && <em>No releases yet</em>}
                  </div>
                </article>
              ))}
              {(projectsByCategory[category.id] || []).length === 0 && (
                <div className="repository-empty">No projects in this category.</div>
              )}
            </div>
          </section>
        ))}
      </div>
    </div>
  )
}
