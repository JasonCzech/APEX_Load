import './ImportWorkflowGuide.css'

interface Props {
  stage: 'source' | 'review' | 'saved'
  reviewOnly?: boolean
}

/** Shared orientation; conversion and inspection never imply a successful replay. */
export default function ImportWorkflowGuide({ stage, reviewOnly = false }: Props) {
  const steps = reviewOnly
    ? ['Provide source', 'Review findings']
    : ['Provide source', 'Review and resolve', 'Save scripts', 'Debug one iteration']
  const current = stage === 'source' ? 0 : stage === 'review' ? 1 : 3
  return (
    <div className="import-workflow-guide">
      <ol aria-label={reviewOnly ? 'Source review workflow' : 'Import workflow'}>
        {steps.map((label, index) => (
          <li key={label} aria-current={index === current ? 'step' : undefined}>
            <span aria-hidden="true">{index + 1}.</span> {label}
          </li>
        ))}
      </ol>
      <p>{reviewOnly
        ? 'Review only: this page produces findings and next actions. It does not create or save a script.'
        : 'Review the generated output before saving. Import checks do not run your script; debug it against a controlled target after saving.'}</p>
    </div>
  )
}
