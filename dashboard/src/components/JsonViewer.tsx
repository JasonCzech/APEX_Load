import React, { useState, useMemo, useEffect } from 'react'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import './JsonViewer.css'

interface JsonViewerProps {
  data: unknown
  defaultExpandedDepth?: number
}

function getType(value: unknown): string {
  if (value === null) return 'null'
  if (Array.isArray(value)) return 'array'
  return typeof value
}

function formatPathKey(key: string): string {
  return /^[a-zA-Z_$][a-zA-Z0-9_$]*$/.test(key) ? `.${key}` : `[${JSON.stringify(key)}]`
}

interface JsonNodeProps {
  name?: string
  value: unknown
  path: string
  depth: number
  isLast: boolean
  defaultExpandedDepth: number
  visiblePaths: Set<string> | null
  lowerFilter: string
  forceToggleNonce: number
  forceToggleDirection: 'expand' | 'collapse' | null
}

function HighlightText({ text, query }: { text: string; query: string }) {
  if (!query) return <>{text}</>
  const idx = text.toLowerCase().indexOf(query)
  if (idx === -1) return <>{text}</>
  return (
    <>
      {text.slice(0, idx)}
      <mark className="json-viewer-highlight">{text.slice(idx, idx + query.length)}</mark>
      {text.slice(idx + query.length)}
    </>
  )
}

function JsonNode({ name, value, path, depth, isLast, defaultExpandedDepth, visiblePaths, lowerFilter, forceToggleNonce, forceToggleDirection }: JsonNodeProps) {
  const [expanded, setExpanded] = useState(depth <= defaultExpandedDepth)
  const [copied, setCopied] = useState(false)
  const type = getType(value)

  useEffect(() => {
    if (forceToggleNonce > 0 && forceToggleDirection) {
      setExpanded(forceToggleDirection === 'expand')
    }
  }, [forceToggleNonce, forceToggleDirection])

  useEffect(() => {
    if (visiblePaths) {
      setExpanded(true)
    } else if (depth > defaultExpandedDepth && expanded) {
      // Just collapse if filter is cleared and we are deeper than default
      setExpanded(false)
    } else if (depth <= defaultExpandedDepth && !expanded) {
      // Expand if filter is cleared and we are within default depth
      setExpanded(true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visiblePaths, defaultExpandedDepth, depth])

  if (visiblePaths && !visiblePaths.has(path)) {
    return null
  }

  const handleCopyPath = async (e: React.MouseEvent) => {
    e.stopPropagation()
    const success = await copyTextToClipboard(path)
    if (success) {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  const renderKey = () => {
    if (name === undefined) return null
    return (
      <span className="json-viewer-key">
        "<HighlightText text={name} query={lowerFilter} />"<span className="json-viewer-punctuation">: </span>
      </span>
    )
  }

  const renderCopyButton = () => {
    if (!path || path === '$') return null
    return (
      <button 
        type="button" 
        className="json-viewer-copy-path" 
        onClick={(e) => void handleCopyPath(e)}
        title="Copy JSONPath"
      >
        {copied ? 'Copied!' : 'Copy Path'}
      </button>
    )
  }

  if (type === 'object' || type === 'array') {
    const isArray = type === 'array'
    const items = isArray ? (value as unknown[]) : Object.entries(value as Record<string, unknown>)
    const isEmpty = isArray ? items.length === 0 : Object.keys(value as object).length === 0

    if (isEmpty) {
      return (
        <div className="json-viewer-line json-viewer-primitive">
          {renderKey()}
          <span className="json-viewer-punctuation">{isArray ? '[]' : '{}'}</span>
          {!isLast && <span className="json-viewer-punctuation">,</span>}
          {renderCopyButton()}
        </div>
      )
    }

    return (
      <div className="json-viewer-node">
        <button 
          type="button"
          className="json-viewer-toggle" 
          onClick={() => setExpanded(!expanded)}
          aria-expanded={expanded}
        >
          {expanded ? '▾' : '▸'}
        </button>
        <div className="json-viewer-line">
          {renderKey()}
          <span className="json-viewer-punctuation">{isArray ? '[' : '{'}</span>
          {!expanded && (
            <span className="json-viewer-summary" onClick={() => setExpanded(true)}>
              {isArray ? `${items.length} items` : `${Object.keys(value as object).length} keys`}
            </span>
          )}
          {!expanded && <span className="json-viewer-punctuation">{isArray ? ']' : '}'}</span>}
          {!expanded && !isLast && <span className="json-viewer-punctuation">,</span>}
          {renderCopyButton()}
        </div>
        
        {expanded && (
          <div className="json-viewer-children">
            {isArray 
              ? (items as unknown[]).map((item, idx) => (
                  <JsonNode 
                    key={idx} 
                    value={item} 
                    path={`${path}[${idx}]`} 
                    depth={depth + 1} 
                    isLast={idx === items.length - 1} 
                    defaultExpandedDepth={defaultExpandedDepth}
                    visiblePaths={visiblePaths}
                    lowerFilter={lowerFilter}
                    forceToggleNonce={forceToggleNonce}
                    forceToggleDirection={forceToggleDirection}
                  />
                ))
              : (items as [string, unknown][]).map(([key, val], idx) => (
                  <JsonNode 
                    key={key} 
                    name={key} 
                    value={val} 
                    path={`${path}${formatPathKey(key)}`} 
                    depth={depth + 1} 
                    isLast={idx === items.length - 1} 
                    defaultExpandedDepth={defaultExpandedDepth}
                    visiblePaths={visiblePaths}
                    lowerFilter={lowerFilter}
                    forceToggleNonce={forceToggleNonce}
                    forceToggleDirection={forceToggleDirection}
                  />
                ))
            }
          </div>
        )}
        {expanded && (
          <div className="json-viewer-line">
            <span className="json-viewer-punctuation">{isArray ? ']' : '}'}</span>
            {!isLast && <span className="json-viewer-punctuation">,</span>}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="json-viewer-line json-viewer-primitive">
      {renderKey()}
      <span className={`json-viewer-${type}`}>
        {type === 'string' ? '"' : ''}
        <HighlightText text={String(value)} query={lowerFilter} />
        {type === 'string' ? '"' : ''}
      </span>
      {!isLast && <span className="json-viewer-punctuation">,</span>}
      {renderCopyButton()}
    </div>
  )
}

export default function JsonViewer({ data, defaultExpandedDepth = 2 }: JsonViewerProps) {
  const [filterText, setFilterText] = useState('')
  const [forceToggleNonce, setForceToggleNonce] = useState(0)
  const [forceToggleDirection, setForceToggleDirection] = useState<'expand' | 'collapse' | null>(null)
  const lowerFilter = filterText.toLowerCase()

  const handleFoldAll = () => {
    setForceToggleDirection('collapse')
    setForceToggleNonce(prev => prev + 1)
  }

  const handleUnfoldAll = () => {
    setForceToggleDirection('expand')
    setForceToggleNonce(prev => prev + 1)
  }

  const visiblePaths = useMemo(() => {
    if (!lowerFilter) return null
    const paths = new Set<string>()

    function traverse(value: unknown, currentPath: string, name?: string): boolean {
      let matches = false
      if (name && name.toLowerCase().includes(lowerFilter)) matches = true

      if (value === null) {
        if ('null'.includes(lowerFilter)) matches = true
      } else if (Array.isArray(value)) {
        for (let i = 0; i < value.length; i++) {
          const childPath = `${currentPath}[${i}]`
          if (traverse(value[i], childPath)) matches = true
        }
      } else if (typeof value === 'object') {
        for (const [k, v] of Object.entries(value)) {
          const childPath = `${currentPath}${formatPathKey(k)}`
          if (traverse(v, childPath, k)) matches = true
        }
      } else {
        if (String(value).toLowerCase().includes(lowerFilter)) matches = true
      }

      if (matches) {
        paths.add(currentPath)
      }
      return matches
    }

    traverse(data, '$')
    return paths
  }, [data, lowerFilter])

  return (
    <div className="json-viewer">
      <div className="json-viewer-toolbar">
        <input 
          type="text" 
          className="form-control input-sm json-viewer-filter" 
          placeholder="Filter JSON..." 
          value={filterText}
          onChange={(e) => setFilterText(e.target.value)}
        />
        <div className="json-viewer-toolbar-actions">
          <button type="button" className="btn btn-sm btn-ghost" onClick={handleUnfoldAll}>
            Unfold All
          </button>
          <button type="button" className="btn btn-sm btn-ghost" onClick={handleFoldAll}>
            Fold All
          </button>
        </div>
      </div>
      <div className="json-viewer-body">
        {visiblePaths && visiblePaths.size === 0 ? (
          <div className="text-muted p-2 text-sm">No matches found.</div>
        ) : (
          <JsonNode 
            value={data} 
            path="$" 
            depth={0} 
            isLast={true} 
            defaultExpandedDepth={defaultExpandedDepth} 
            visiblePaths={visiblePaths}
            lowerFilter={lowerFilter}
            forceToggleNonce={forceToggleNonce}
            forceToggleDirection={forceToggleDirection}
          />
        )}
      </div>
    </div>
  )
}
