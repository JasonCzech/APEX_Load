import { useHTTP3Capability } from '../hooks/useHTTP3Capability'
import { HTTP3_EXECUTION_NOTE, isHTTP3Policy } from './httpVersionModel'

/**
 * Says whether this installation can run an HTTP/3 policy. It never blocks
 * saving: scripts keep HTTP/3 policies on any installation, and admission
 * decides whether a run may use them.
 */
export function HTTP3ExecutionNote({ policy, as: Tag = 'span' }: { policy?: string; as?: 'span' | 'p' }) {
  const isHTTP3 = isHTTP3Policy(policy)
  const capability = useHTTP3Capability(isHTTP3)
  if (!isHTTP3) return null
  let text = HTTP3_EXECUTION_NOTE
  if (capability) {
    const runnable = policy === '3' ? capability.exact : capability.upgrade
    if (runnable) text = 'HTTP/3 execution is enabled on this controller. Distributed runs also need it on every generator, which admission checks.'
    else if (policy === 'auto+h3' && capability.exact) text = 'This controller runs exact HTTP/3 (3) but not auto+h3 yet, so runs with this policy are refused. The script still saves.'
    else text = 'HTTP/3 execution is not enabled on this installation, so runs with this policy are refused until an operator enables it. The script still saves.'
  }
  return <Tag className="http3-execution-note" data-capability={capability ? (policy === '3' ? capability.exact : capability.upgrade) ? 'enabled' : 'disabled' : 'unknown'}>{Tag === 'span' ? ` ${text}` : text}</Tag>
}
