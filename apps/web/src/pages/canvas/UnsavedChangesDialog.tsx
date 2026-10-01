import { useEffect, useRef, useState } from 'react';

// Shown while the router blocks navigation away from a dirty draft.
// A native modal <dialog> traps focus and makes the canvas behind it inert.
// Stay is the default (focused, Escape); a failed save keeps the user here.
export function UnsavedChangesDialog({ canSave, reason, onStay, onDiscard, onSave }: {
  canSave: boolean;
  reason?: string;
  onStay: () => void;
  onDiscard: () => void;
  onSave: () => Promise<void>;
}) {
  const [saving, setSaving] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const stay = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!element.open) element.showModal();
    stay.current?.focus();
    // Unmounting (Stay, Discard or a successful save) closes the modal and
    // returns focus to whatever triggered the blocked navigation.
    return () => {
      if (element.open) element.close();
      previous?.focus();
    };
  }, []);

  return (
    <dialog ref={dialog} role="alertdialog" aria-labelledby="unsaved-title" aria-describedby="unsaved-desc"
      onCancel={event => { event.preventDefault(); onStay(); }}
      className="m-auto w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-5 shadow-xl backdrop:bg-black/40 dark:border-white/10 dark:bg-[#11141d]">
      <h2 id="unsaved-title" className="text-sm font-semibold text-gray-900 dark:text-white/90">Unsaved changes</h2>
      <p id="unsaved-desc" className="mt-2 text-xs text-gray-600 dark:text-white/65">
        This pipeline has changes that are not saved. Leaving now discards them.
        {!canSave && reason && <span className="mt-2 block text-red-600 dark:text-red-300">Cannot save yet: {reason}</span>}
      </p>
      <div className="mt-4 flex justify-end gap-2">
        <button className="glass-btn-ghost text-xs" onClick={onDiscard}>Discard changes</button>
        <button ref={stay} className="glass-btn-ghost text-xs" onClick={onStay}>Stay</button>
        <button className="glass-btn-primary text-xs disabled:cursor-not-allowed disabled:opacity-40" disabled={!canSave || saving}
          onClick={async () => { setSaving(true); try { await onSave(); } finally { setSaving(false); } }}>
          {saving ? 'Saving…' : 'Save and leave'}
        </button>
      </div>
    </dialog>
  );
}
