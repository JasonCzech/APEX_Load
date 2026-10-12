import type { ScriptConfig, ScriptActionDefinition } from '../types/scripts'

export const NATIVE_TABLE_OPERATIONS = ['read', 'next', 'push', 'pop', 'lease', 'renew', 'ack', 'release', 'update', 'increment', 'clear', 'rotate', 'create_column', 'read_column'] as const
export type NativeTableDeclaration = NonNullable<NonNullable<ScriptConfig['native_services']>['live_tables']>[number]

export function nativeDeclarationErrors(declarations: NativeTableDeclaration[]): string[] {
  const errors: string[] = []
  const aliases = new Set<string>()
  if (declarations.length > 16) errors.push('Declare at most 16 live-table aliases.')
  declarations.forEach((declaration, index) => {
    if (!/^[A-Za-z_][\w-]{0,63}$/.test(declaration.alias)) errors.push(`Declaration ${index + 1}: use a valid alias of at most 64 characters.`)
    if (aliases.has(declaration.alias.toLowerCase())) errors.push(`Duplicate live-table declaration: ${declaration.alias}.`)
    aliases.add(declaration.alias.toLowerCase())
    if (new Set(declaration.operations).size !== declaration.operations.length) errors.push(`Declaration ${index + 1}: select distinct operations.`)
    if (!declaration.operations.length) errors.push(`Declaration ${index + 1}: select at least one allowed operation.`)
    if (declaration.operations.some(operation => !(NATIVE_TABLE_OPERATIONS as readonly string[]).includes(operation))) errors.push(`Declaration ${index + 1}: an operation is not supported.`)
  })
  return errors
}

export function nativeJavaScriptTemplate(kind: 'producer' | 'consumer' | 'update' | 'lease'): ScriptActionDefinition {
  const sources = {
    producer: "const result = apex.data.table('work').push({id: input.id});\nreturn {status: result.status, revision: result.revision};",
    consumer: "const result = apex.data.table('work').pop();\nreturn {status: result.status, row: result.row};",
    update: "const result = apex.data.call({alias: 'work', operation: 'update', record_id: input.record_id, expected_version: input.version, values: {status: 'ready'}});\nreturn {status: result.status, revision: result.revision};",
    lease: "const table = apex.data.table('work');\nconst result = table.lease();\nif (result.lease_ref) table.ack(result.lease_ref);\nreturn {status: result.status, row: result.row};",
  }
  return { name: `Native ${kind}`, type: 'native_javascript', target: '', params: { source: sources[kind], inputs: kind === 'producer' ? { id: '{{var.id}}' } : kind === 'update' ? { record_id: '{{var.record_id}}', version: '{{var.version}}' } : {}, result_prefix: kind, timeout_ms: 5000 } }
}

export const NATIVE_JAVA_EXAMPLES = [
  { name: 'Producer', operations: ['push'], body: `Data.Result result = Data.table("work").push(Collections.singletonMap("id", "C001"));
        return "ok".equals(result.status()) ? 0 : -1;` },
  { name: 'Consumer', operations: ['pop'], body: `Data.Result result = Data.table("work").pop();
        if (!"ok".equals(result.status())) return -1;
        String id = result.row().values().get("id");
        // Use id in the surrounding native script workflow.
        return id == null ? -1 : 0;` },
  { name: 'Conditional update', operations: ['next', 'update'], body: `Data.Table table = Data.table("work");
        Data.Result selected = table.next();
        if (!"ok".equals(selected.status())) return -1;
        Data.Row row = selected.row();
        Data.Result result = table.update(row.id(), row.version(), Collections.singletonMap("id", "C002"));
        return "ok".equals(result.status()) ? 0 : -1;` },
  { name: 'Lease and acknowledge', operations: ['lease', 'ack'], body: `Data.Table table = Data.table("work");
        Data.Result result = table.lease(1000, 30000);
        if (!"ok".equals(result.status())) return -1;
        // Complete the work before acknowledgment. The runtime renews the lease.
        Data.Result acknowledged = table.ack(result.leaseRef());
        return "ok".equals(acknowledged.status()) ? 0 : -1;` },
].map(example => ({ ...example, source: `import apexload.data.Data;
import java.util.Collections;

public class Actions {
    public int init() { return 0; }
    public int action() {
        ${example.body}
    }
    public int end() { return 0; }
}` }))
