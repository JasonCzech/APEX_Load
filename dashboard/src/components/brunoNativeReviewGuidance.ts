export type NativeReviewGuidance = {
  title: string
  impact: string
  resolution: string
}

export function nativeReviewGuidance(code: string): NativeReviewGuidance {
  switch (code) {
    case 'bruno_form_template_review_required':
      return {
        title: 'Form body template encoding differs',
        impact: 'Bruno expands the form value before URL encoding, while the native draft cannot guarantee the same wire value after runtime template expansion. Replay is blocked to prevent sending changed form data.',
        resolution: 'Translate the form body so each runtime value is URL-encoded at the correct stage, compare the resulting wire body with Bruno, and then remove the script’s review gate.',
      }
    case 'bruno_multipart_template_and_file_review_required':
      return {
        title: 'Multipart templates and files need translation',
        impact: 'Runtime MIME escaping cannot be preserved automatically and imported file parts were omitted, so the draft would send an incomplete multipart body. Replay is blocked to prevent changed or missing payload data.',
        resolution: 'Recreate the file parts with governed APEX file assets, translate and MIME-escape each runtime field, verify the complete multipart wire body, and then remove the script’s review gate.',
      }
    case 'bruno_multipart_template_review_required':
      return {
        title: 'Multipart template escaping differs',
        impact: 'The native draft cannot reproduce Bruno’s MIME escaping after runtime template expansion, so expanded field values could change the multipart wire body. Replay is blocked to prevent sending altered data.',
        resolution: 'Translate and MIME-escape each runtime multipart field, compare the resulting wire body with Bruno, and then remove the script’s review gate.',
      }
    case 'bruno_multipart_file_review_required':
      return {
        title: 'Multipart file parts were omitted',
        impact: 'Bruno file parts cannot be embedded automatically in the native draft, so replay would send an incomplete multipart request. Replay is blocked to prevent a misleading or destructive partial request.',
        resolution: 'Add the intended files as governed APEX file assets, bind them to the multipart action, verify the complete request, and then remove the script’s review gate.',
      }
    case 'bruno_graphql_body_review_required':
      return {
        title: 'GraphQL body needs translation',
        impact: 'The Bruno GraphQL body was preserved for review but was not proven equivalent to a native HTTP request body. Replay is blocked to prevent sending a changed query, operation name, or variables.',
        resolution: 'Translate the GraphQL operation and variables into a supported native HTTP body, compare the resulting request with Bruno, and then remove the script’s review gate.',
      }
    case 'bruno_url_secret_encoding_review_required':
      return {
        title: 'Credential URL encoding cannot be preserved',
        impact: 'APEX cannot safely reproduce Bruno’s URL-component encoding for governed credential values, so replay could send the wrong credential representation or expose it in an unsafe transport.',
        resolution: 'Move or encode the credential with a supported governed binding, verify the final URL without exposing the value, and then remove the script’s review gate.',
      }
    case 'bruno_javascript_review_required':
      return {
        title: 'Unsupported Bruno JavaScript',
        impact: 'The hook was not translated, so its assertions, variable updates, or other behavior will not run in APEX. Replay is blocked to prevent an incomplete script from producing misleading results.',
        resolution: 'Translate the hook into supported APEX assertions, extractors, or actions, verify the result, and then remove the script’s review gate.',
      }
    case 'bruno_sensitive_header_resolution_review_required':
      return {
        title: 'Unresolved sensitive header template',
        impact: 'APEX could not reconstruct the sensitive header safely, so the imported draft does not contain a replay-ready value. Replay is blocked to prevent an incorrect or unsafe credential from being sent.',
        resolution: 'Bind and verify the governed whole-header value, confirm that it matches the intended Bruno request, and then remove the script’s review gate.',
      }
    case 'bruno_url_template_encoding_review_required':
      return {
        title: 'URL template encoding differs',
        impact: 'APEX cannot apply Bruno’s URL-component encoding after runtime path or query template expansion, so replay could target a different URL or send different query values.',
        resolution: 'Translate the templated path and query values with explicit supported encoding, compare the final URL with Bruno, and then remove the script’s review gate.',
      }
    case 'bruno_json_secret_escaping_review_required':
      return {
        title: 'JSON credential escaping is not guaranteed',
        impact: 'Governed credential values are interpolated as raw text, so quotes or control characters could produce invalid JSON or change the request body. Replay is blocked to protect credential integrity.',
        resolution: 'Bind the credential through a JSON-safe supported action or explicitly escape it, validate the final JSON without disclosing the value, and then remove the script’s review gate.',
      }
    case 'bruno_xml_unsupported_markup_review_required':
      return {
        title: 'Credential-bearing XML was redacted',
        impact: 'Unsupported credential-bearing XML markup was removed from the native draft, so replay would send a body that is not wire-equivalent to Bruno.',
        resolution: 'Rebuild the XML with a governed credential binding, verify the complete wire body without exposing the credential, and then remove the script’s review gate.',
      }
    case 'bruno_auth_review_required':
      return {
        title: 'Authentication mode is unsupported',
        impact: 'The Bruno authentication behavior cannot be represented by the native HTTP DSL, so the draft would not authenticate in the same way. Replay is blocked to prevent incorrect or unauthorized requests.',
        resolution: 'Translate the authentication flow into a supported governed APEX mechanism, verify it against the intended target, and then remove the script’s review gate.',
      }
    case 'bruno_max_redirects_clamped':
      return {
        title: 'Redirect limit was reduced',
        impact: 'The Bruno redirect limit exceeds the APEX maximum and was clamped in the draft, so replay can stop earlier and produce different responses or assertions.',
        resolution: 'Confirm the reduced redirect limit is acceptable or redesign the flow within the supported maximum, verify the resulting behavior, and then remove the script’s review gate.',
      }
    case 'bruno_encode_url_false_review_required':
      return {
        title: 'Unencoded URL behavior is not guaranteed',
        impact: 'Bruno was configured with encodeUrl=false, but the native runtime cannot guarantee the same raw URL bytes. Replay is blocked to prevent targeting a different resource or sending altered query data.',
        resolution: 'Translate the request to a supported explicit URL representation, compare the final wire URL with Bruno, and then remove the script’s review gate.',
      }
    default:
      return {
        title: 'Imported behavior requires manual translation',
        impact: 'APEX preserved the request as an incomplete draft but cannot guarantee behavior equivalent to the Bruno source. Replay remains blocked to prevent misleading test results.',
        resolution: 'Review the finding, translate or correct the unsupported behavior, verify the result, and then remove the script’s review gate.',
      }
  }
}
