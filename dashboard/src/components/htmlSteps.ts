// HTML-mode steps (LoadRunner web_link, web_image, web_submit_form,
// web_image_check) carry params.html_step and resolve against the page the
// virtual user last loaded, so they have no meaningful target.

type HTMLStep = {
  kind?: string
  text?: string
  alt?: string
  src?: string
  ordinal?: number
  action?: string
  fields?: Array<{ name?: string }>
}

export function htmlStepOf(params: Record<string, unknown> | undefined): HTMLStep | undefined {
  const step = params?.html_step
  return step && typeof step === 'object' ? step as HTMLStep : undefined
}

// describeHTMLStep says in one line what the step clicks or submits.
export function describeHTMLStep(step: HTMLStep): string {
  const ordinal = step.ordinal && step.ordinal > 1 ? ` (match ${step.ordinal})` : ''
  const image = [step.alt ? `alt "${step.alt}"` : '', step.src ? `src ${step.src}` : ''].filter(Boolean).join(', ')
  switch (step.kind) {
    case 'link':
      return `Follows the link "${step.text ?? ''}"${ordinal} on the current page.`
    case 'image':
      return `Clicks the image with ${image}${ordinal} on the current page.`
    case 'image_check':
      return `Checks that the current page shows the image with ${image}; no request is sent.`
    case 'form': {
      const fields = (step.fields ?? []).map(field => field.name).filter(Boolean)
      const which = fields.length > 0 ? `with fields ${fields.join(', ')}` : 'on the page'
      return `Submits the form ${which}${step.action ? ` (action ${step.action})` : ''}${ordinal}, with the page's own hidden fields, URL and method.`
    }
    default:
      return 'Resolved against the current page at replay.'
  }
}
