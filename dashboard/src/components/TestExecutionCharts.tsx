import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

interface ChartConfig {
  id: string
  title: string
  type: 'area' | 'line-multi' | 'line-latency' | 'area-multi' | 'line-io' | 'line-network'
  dataKey?: string
  color?: string
  totalKey?: string
  keys?: Array<{ k: string; n: string; c: string }>
  domain?: [number, string] | [number, number]
}

interface Props {
  filteredData: any[]
  expandedChart: string | null
  onExpandedChartChange: (value: string | null) => void
  onChartMouseDown: (activeLabel?: string | number) => void
  onChartMouseMove: (activeLabel?: string | number) => void
  onZoom: () => void
  showRefArea: boolean
  refAreaLeft: string | number
  refAreaEnd: string | number
  isMultiGroup: boolean
  groupNames: string[]
  groupColors: string[]
}

const chartConfigs: ChartConfig[] = [
  { id: 'vusers', title: 'Virtual Users', type: 'area', dataKey: 'vusers', color: 'var(--chart-1)' },
  { id: 'tps', title: 'Transactions Per Second', type: 'line-multi', totalKey: 'tps' },
  { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' },
  { id: 'errors', title: 'Error Rate (%)', type: 'area', dataKey: 'errorPct', color: 'var(--chart-4)', domain: [0, 'auto'] },
  {
    id: 'cpu',
    title: 'CPU Usage (%)',
    type: 'area-multi',
    keys: [
      { k: 'cpuProcess', n: 'VUsers', c: 'var(--chart-1)' },
      { k: 'cpuFree', n: 'System', c: 'var(--chart-2)' },
    ],
    domain: [0, 100],
  },
  {
    id: 'mem',
    title: 'Memory Usage (MB)',
    type: 'area-multi',
    keys: [
      { k: 'memProcess', n: 'VUsers', c: 'var(--chart-4)' },
      { k: 'memFree', n: 'System', c: 'var(--chart-5)' },
    ],
  },
  { id: 'io', title: 'Network & Disk I/O (KB/s)', type: 'line-io' },
  { id: 'network_latency', title: 'Route Latency (ms)', type: 'line-network' },
]

export default function TestExecutionCharts({
  filteredData,
  expandedChart,
  onExpandedChartChange,
  onChartMouseDown,
  onChartMouseMove,
  onZoom,
  showRefArea,
  refAreaLeft,
  refAreaEnd,
  isMultiGroup,
  groupNames,
  groupColors,
}: Props) {
  if (filteredData.length <= 1) {
    return null
  }

  const latest = filteredData[filteredData.length - 1] || {}
  const sortedCharts = [...chartConfigs].sort((a, b) => {
    if (a.id === expandedChart) return -1
    if (b.id === expandedChart) return 1
    return 0
  })

  return (
    <div className="charts-grid">
      {sortedCharts.map((chart) => (
        <div key={chart.id} className={`chart-card ${expandedChart === chart.id ? 'expanded' : ''}`}>
          <div className="chart-card-header">
            <h3 className="chart-title">{chart.title}</h3>
            <button className="btn-icon btn-expand" onClick={() => onExpandedChartChange(expandedChart === chart.id ? null : chart.id)}>
              {expandedChart === chart.id ? '↙' : '↗'}
            </button>
          </div>
          <ResponsiveContainer width="100%" height={expandedChart === chart.id ? 400 : 200}>
            {chart.type === 'area' ? (
              <AreaChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} domain={chart.domain as any} />
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Area type="monotone" dataKey={chart.dataKey!} name="Total" stroke={chart.color!} fill={chart.color!} fillOpacity={0.15} isAnimationActive={false} />
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </AreaChart>
            ) : chart.type === 'line-multi' ? (
              <LineChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} />
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                {isMultiGroup && <Legend verticalAlign="bottom" height={36} />}
                <Line type="monotone" dataKey={chart.totalKey!} name="Total" stroke="var(--text-muted)" dot={false} strokeWidth={2} strokeDasharray={isMultiGroup ? '4 2' : undefined} isAnimationActive={false} />
                {isMultiGroup && groupNames.map((name, index) => (
                  <Line key={name} type="monotone" dataKey={`${name}_tps`} name={name} stroke={groupColors[index % groupColors.length]} dot={false} isAnimationActive={false} />
                ))}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : chart.type === 'line-latency' ? (
              <LineChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} />
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Legend verticalAlign="bottom" height={36} />
                <Line type="monotone" dataKey="p50" name="P50" stroke="var(--chart-5)" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line type="monotone" dataKey="p90" name="P90" stroke="var(--chart-3)" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line type="monotone" dataKey="p99" name="P99" stroke="var(--chart-4)" dot={false} strokeWidth={1.5} strokeDasharray="4 2" isAnimationActive={false} />
                {isMultiGroup && groupNames.map((name, index) => (
                  <Line key={name} type="monotone" dataKey={`${name}_avgMs`} name={`${name} avg`} stroke={groupColors[index % groupColors.length]} dot={false} strokeWidth={1.5} strokeDasharray="2 2" isAnimationActive={false} />
                ))}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : chart.type === 'area-multi' ? (
              <AreaChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} domain={chart.domain as any} />
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Legend verticalAlign="bottom" height={36} />
                {chart.keys!.map((key) => (
                  <Area key={key.k} type="monotone" dataKey={key.k} name={key.n} stroke={key.c} fill={key.c} fillOpacity={0.2} isAnimationActive={false} />
                ))}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </AreaChart>
            ) : chart.type === 'line-network' ? (
              <LineChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} />
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Legend verticalAlign="bottom" height={36} />
                {Object.keys(latest)
                  .filter(key => key.startsWith('latency_'))
                  .map((key, index) => (
                    <Line
                      key={key}
                      type="monotone"
                      dataKey={key}
                      name={key.replace('latency_', '')}
                      stroke={groupColors[index % groupColors.length]}
                      dot={false}
                      strokeWidth={2}
                      isAnimationActive={false}
                    />
                  ))}
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            ) : (
              <LineChart
                data={filteredData}
                syncId="metrics"
                onMouseDown={(event) => onChartMouseDown(event?.activeLabel)}
                onMouseMove={(event) => onChartMouseMove(event?.activeLabel)}
                onMouseUp={onZoom}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} />
                <Tooltip
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                  labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                  isAnimationActive={false}
                />
                <Legend />
                <Line type="monotone" dataKey="netSent" name="Net Egress" stroke="var(--chart-1)" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line type="monotone" dataKey="netRecv" name="Net Ingress" stroke="var(--chart-2)" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line type="monotone" dataKey="diskRead" name="Disk Read" stroke="var(--chart-3)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />
                <Line type="monotone" dataKey="diskWrite" name="Disk Write" stroke="var(--chart-6)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />
                {showRefArea && (
                  <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                )}
              </LineChart>
            )}
          </ResponsiveContainer>

          <div className="chart-metrics-footer">
            {chart.id === 'vusers' && (
              <div className="footer-metric">
                <span className="dot" style={{ background: 'var(--chart-1)' }}></span>
                <span className="label">Active:</span>
                <span className="value">{latest.vusers}</span>
              </div>
            )}
            {chart.id === 'tps' && (
              <>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--text-muted)' }}></span>
                  <span className="label">Total:</span>
                  <span className="value">{latest.tps}</span>
                </div>
                {isMultiGroup && groupNames.map((name, index) => (
                  <div key={name} className="footer-metric">
                    <span className="dot" style={{ background: groupColors[index % groupColors.length] }}></span>
                    <span className="label">{name}:</span>
                    <span className="value">{latest[`${name}_tps`]}</span>
                  </div>
                ))}
              </>
            )}
            {chart.id === 'latency' && (
              <>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-5)' }}></span>
                  <span className="label">P50:</span>
                  <span className="value">{latest.p50}ms</span>
                </div>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-3)' }}></span>
                  <span className="label">P90:</span>
                  <span className="value">{latest.p90}ms</span>
                </div>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-4)' }}></span>
                  <span className="label">P99:</span>
                  <span className="value">{latest.p99}ms</span>
                </div>
              </>
            )}
            {chart.id === 'errors' && (
              <div className="footer-metric">
                <span className="dot" style={{ background: 'var(--chart-4)' }}></span>
                <span className="label">Current:</span>
                <span className="value">{latest.errorPct}%</span>
              </div>
            )}
            {chart.type === 'area-multi' && chart.keys?.map((key) => (
              <div key={key.k} className="footer-metric">
                <span className="dot" style={{ background: key.c }}></span>
                <span className="label">{key.n}:</span>
                <span className="value">{latest[key.k]}{chart.id === 'cpu' ? '%' : ' MB'}</span>
              </div>
            ))}
            {chart.id === 'io' && (
              <>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-1)' }}></span>
                  <span className="label">Net Out:</span>
                  <span className="value">{latest.netSent} KB/s</span>
                </div>
                <div className="footer-metric">
                  <span className="dot" style={{ background: 'var(--chart-2)' }}></span>
                  <span className="label">Net In:</span>
                  <span className="value">{latest.netRecv} KB/s</span>
                </div>
              </>
            )}
            {chart.id === 'network_latency' && (
              <>
                {Object.keys(latest)
                  .filter(key => key.startsWith('latency_'))
                  .map((key, index) => (
                    <div key={key} className="footer-metric">
                      <span className="dot" style={{ background: groupColors[index % groupColors.length] }}></span>
                      <span className="label">{key.replace('latency_', '')}:</span>
                      <span className="value">{latest[key]}ms</span>
                    </div>
                  ))}
              </>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}