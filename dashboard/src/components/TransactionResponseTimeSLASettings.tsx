import { useId } from 'react'

interface SLAGroupOption {
	name: string
	scriptName?: string
	overrideMs?: number
}

interface Props {
	defaultMs?: number
	groups: SLAGroupOption[]
	onDefaultChange: (value?: number) => void
	onGroupOverrideChange: (index: number, value?: number) => void
}

function secondsValue(milliseconds?: number): string {
	return milliseconds === undefined ? '' : String(milliseconds / 1000)
}

function millisecondsValue(raw: string): number | undefined {
	if (raw.trim() === '') return undefined
	const seconds = Number(raw)
	return Number.isFinite(seconds) ? seconds * 1000 : undefined
}

function formatSeconds(milliseconds: number): string {
	return `${milliseconds / 1000}s`
}

export default function TransactionResponseTimeSLASettings({
	defaultMs,
	groups,
	onDefaultChange,
	onGroupOverrideChange,
}: Props) {
	const titleId = useId()
	const defaultHelpId = useId()
	const covered = groups.filter(group => group.overrideMs !== undefined || defaultMs !== undefined).length
	const overridden = groups.filter(group => group.overrideMs !== undefined).length

	return (
		<section className="scenario-subpanel transaction-sla-settings" aria-labelledby={titleId}>
			<div className="scenario-assignment-header">
				<div>
					<h4 id={titleId}>Transaction response-time SLA</h4>
					<p>Require at least 95% of successful named business transactions to finish within the configured time. Request/action rows and failed samples are excluded.</p>
				</div>
				<span className="transaction-sla-coverage">{covered} of {groups.length} groups covered · {overridden} override{overridden === 1 ? '' : 's'}</span>
			</div>

			<label className="scenario-field transaction-sla-default">
				<span>Default for all groups (seconds)</span>
				<input
					className="settings-input"
					type="number"
					min="0"
					step="any"
					value={secondsValue(defaultMs)}
					onChange={event => onDefaultChange(millisecondsValue(event.currentTarget.value))}
					placeholder="e.g. 3"
					aria-label="Default transaction SLA in seconds"
					aria-describedby={defaultHelpId}
				/>
				<small id={defaultHelpId}>Leave empty to configure only selected groups.</small>
			</label>

			<div className="transaction-sla-group-list">
				{groups.map((group, index) => {
					const label = group.name.trim() || `Group ${index + 1}`
					const hasOverride = group.overrideMs !== undefined
					return (
						<div className="transaction-sla-group-row" key={`${label}-${index}`}>
							<div>
								<strong>{label}</strong>
								<span>{group.scriptName || 'Unassigned script'}</span>
							</div>
							<label className="transaction-sla-mode">
								<span className="sr-only">{label} SLA source</span>
								<select
									className="settings-input"
									value={hasOverride ? 'override' : 'default'}
									onChange={event => onGroupOverrideChange(index, event.currentTarget.value === 'override' ? (defaultMs ?? 3000) : undefined)}
									aria-label={`${label} SLA source`}
								>
									<option value="default">Use default</option>
									<option value="override">Override</option>
								</select>
							</label>
							{hasOverride ? (
								<label className="transaction-sla-override-input">
									<span className="sr-only">{label} SLA in seconds</span>
									<input
										className="settings-input"
										type="number"
										min="0"
										step="any"
										value={secondsValue(group.overrideMs)}
										onChange={event => onGroupOverrideChange(index, millisecondsValue(event.currentTarget.value))}
									/>
									<span>seconds</span>
								</label>
							) : (
								<span className="transaction-sla-effective">{defaultMs !== undefined ? `Inherits ${formatSeconds(defaultMs)}` : 'No SLA'}</span>
							)}
						</div>
					)
				})}
			</div>
		</section>
	)
}
