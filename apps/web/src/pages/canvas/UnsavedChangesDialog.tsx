import { useState } from 'react';

// Shown while the router blocks navigation away from a dirty draft.
// Stay is the default (focused, Escape); a failed save keeps the user here.
export function UnsavedChangesDialog({ canSave, reason, onStay, onDiscard, onSave }: {
  canSave: boolean;
  reason?: string;
  onStay: () => void;
  onDiscard: () => void;
  onSave: () => Promise<void>;
}) {
  const [saving, setSaving] = useState(false);
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onKeyDown={e => { if (e.key === 'Escape') onStay(); }}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="unsaved-title" aria-describedby="unsaved-desc"
        className="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-5 shadow-xl dark:border-white/10 dark:bg-[#11141d]">
        <h2 id="unsaved-title" className="text-sm font-semibold text-gray-900 dark:text-white/90">Unsaved changes</h2>
        <p id="unsaved-desc" className="mt-2 text-xs text-gray-600 dark:text-white/65">
          This pipeline has changes that are not saved. Leaving now discards them.
          {!canSave && reason && <span className="mt-2 block text-red-600 dark:text-red-300">Cannot save yet: {reason}</span>}
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <button className="glass-btn-ghost text-xs" onClick={onDiscard}>Discard changes</button>
          <button className="glass-btn-ghost text-xs" autoFocus onClick={onStay}>Stay</button>
          <button className="glass-btn-primary text-xs disabled:cursor-not-allowed disabled:opacity-40" disabled={!canSave || saving}
            onClick={async () => { setSaving(true); try { await onSave(); } finally { setSaving(false); } }}>
            {saving ? 'Saving…' : 'Save and leave'}
          </button>
        </div>
      </div>
    </div>
  );
}
