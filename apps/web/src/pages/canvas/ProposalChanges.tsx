import type { ProposalChange } from '../../utils/pipelineProposal';

export function ProposalChanges({ changes }: { changes: ProposalChange[] }) {
  return <section aria-labelledby="ai-changes-heading" className="space-y-2 text-xs">
    <h3 id="ai-changes-heading" className="font-semibold text-gray-900 dark:text-white/90">Review changes</h3>
    <p id="ai-changes-summary" className="text-gray-600 dark:text-white/65">
      {changes.length ? `${changes.length} changed ${changes.length === 1 ? 'item' : 'items'}. Expand each item for before and after values.` : 'No pipeline changes.'}
      {' '}Unlisted settings stay unchanged. Sensitive and unrecognized values are hidden.
    </p>
    <div className="max-h-72 space-y-2 overflow-y-auto">
      {changes.map(change => <details key={change.key} className="rounded border border-gray-200 p-2 dark:border-white/15">
        <summary className="cursor-pointer break-words font-medium text-gray-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-brand-500 dark:text-white/85">
          {change.action}: {change.subject} · {change.fields.length} {change.fields.length === 1 ? 'field' : 'fields'}
        </summary>
        <ul className="mt-2 space-y-2" aria-label={`${change.subject} field changes`}>
          {change.fields.map(field => <li key={field.field} className="break-words text-gray-700 dark:text-white/75">
            <div className="font-medium">{field.action}: {field.field}</div>
            <div>Before: {field.before}</div>
            <div>After: {field.after}</div>
          </li>)}
        </ul>
      </details>)}
    </div>
  </section>;
}
