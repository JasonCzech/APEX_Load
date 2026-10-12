import type { ProjectLiveTableRunReference } from '../types/projectLiveTables'

export default function ProjectLiveTableReferences({ tables, projectId }: { tables?: ProjectLiveTableRunReference[]; projectId?: string }) {
  if (!tables?.length) return null
  return <section className="project-live-table-references" aria-label="Project table references"><h3>Project table references</h3>
    <p>These tables are shared with other runs. Each reference records this run’s attachment; global row counts and operations are not attributed to this run. Open the project table to inspect its current state with current project permissions.</p>
    <ul>{tables.map(table => <li key={table.attachment_id}>
      {projectId ? <a href={`#/repository?${new URLSearchParams({ setting: 'live-tables', projectId, resourceId: table.table_id })}`}>{table.alias} · {table.group}</a> : <span>{table.alias} · {table.group}</span>}
      {' · '}{table.state.replace(/_/g, ' ')}{' · Recorded revision '}{table.revision}
      <p>Table <code>{table.table_id}</code> · Instance <code>{table.instance_id}</code> · Attachment <code>{table.attachment_id}</code></p>
    </li>)}</ul>
  </section>
}
