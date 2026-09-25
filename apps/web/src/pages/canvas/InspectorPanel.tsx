import { useEffect, useRef } from 'react';
import { Code2, X } from 'lucide-react';
import type { Node } from 'reactflow';
import { MermaidPreview } from '../../components/MermaidPreview';
import { ConfigPanel } from '../../components/canvas/ConfigPanel';

// Right-side slide-out panel: whichever of the three mutually-exclusive modes
// is active — Mermaid structure editor, selected-edge branch condition, or
// selected-node config — shares one header/close button and one width.
export function InspectorPanel({
  open, showMermaid, selected, selectedEdge, onClose, focusOnOpen,
  mermaidDraft, setMermaidDraft, mermaidValid, setMermaidValid, applyMermaid,
  onEdgeConditionChange,
  onNodeChange, onNodeDelete,
}: {
  open: boolean;
  showMermaid: boolean;
  selected: Node | null;
  selectedEdge: any | null;
  onClose: () => void;
  focusOnOpen: boolean;
  mermaidDraft: string;
  setMermaidDraft: (value: string) => void;
  mermaidValid: boolean;
  setMermaidValid: (valid: boolean) => void;
  applyMermaid: () => void;
  onEdgeConditionChange: (id: string, condition: string) => void;
  onNodeChange: (id: string, patch: any) => void;
  onNodeDelete: (id: string) => void;
}) {
  const body = useRef<HTMLDivElement>(null);
  // Keyboard activation moves focus to the first setting; Close returns it to the node.
  useEffect(() => {
    if (open && focusOnOpen) body.current?.querySelector<HTMLElement>('input, select, textarea, button')?.focus();
  }, [open, focusOnOpen, selected?.id]);
  return (
    <aside aria-label="Inspector" onKeyDown={event => { if (open && event.key === 'Escape') onClose(); }}
      className={`flex flex-col flex-none overflow-hidden border-l border-gray-200 dark:border-white/[0.08] bg-white/97 dark:bg-[#0d1018]/94 backdrop-blur-xl transition-[width] duration-200 ease-in-out ${
        open ? 'w-[360px]' : 'w-0'
      }`}
    >
      {open && (
        <>
          <div className="flex h-14 flex-none items-center justify-between border-b border-gray-100 dark:border-white/[0.07] px-4">
            <div>
              <p className="text-xs font-semibold text-gray-900 dark:text-white/85">
                {showMermaid ? 'Mermaid editor' : selectedEdge ? 'Branch condition' : 'Node settings'}
              </p>
              <p className="text-[10px] text-gray-400 dark:text-white/30">
                {showMermaid ? 'Edit graph as code' : selectedEdge ? 'Conditional routing' : 'Configure selected node'}
              </p>
            </div>
            <button aria-label="Close panel" className="icon-button h-8 w-8" onClick={onClose}>
              <X size={15} />
            </button>
          </div>
          <div ref={body} className="flex-1 overflow-auto p-4">
            {showMermaid ? (
              <>
                <textarea className="glass-input h-52 w-full font-mono text-[11px]" aria-label="Mermaid diagram source"
                  value={mermaidDraft} onChange={e => setMermaidDraft(e.target.value)} />
                <button className="glass-btn-primary mt-3 w-full disabled:cursor-not-allowed disabled:opacity-50" disabled={!mermaidValid} onClick={applyMermaid}>
                  <Code2 size={15} /> Apply to canvas
                </button>
                <div className="mt-4 overflow-hidden rounded-[14px] border border-gray-100 dark:border-white/[0.08] bg-gray-50 dark:bg-black/15 p-2">
                  <MermaidPreview source={mermaidDraft} onValidChange={setMermaidValid} />
                </div>
                <p className="mt-2 text-[10px] text-gray-400 dark:text-white/30">Structure only. Node config preserved by matching ID.</p>
              </>
            ) : selectedEdge ? (
              <div>
                <p className="mb-1 text-xs font-semibold text-gray-900 dark:text-white/85">Branch condition</p>
                <p className="mb-3 text-[10px] text-gray-400 dark:text-white/35">Records flow only when true. Blank = always.</p>
                <textarea className="glass-input h-20 w-full font-mono text-[11px]"
                  placeholder="r.amount > 100" aria-label="Branch condition expression"
                  value={selectedEdge.data?.condition ?? ''}
                  onChange={e => onEdgeConditionChange(selectedEdge.id, e.target.value)} />
              </div>
            ) : selected ? (
              <ConfigPanel node={selected} onChange={onNodeChange} onDelete={onNodeDelete} />
            ) : null}
          </div>
        </>
      )}
    </aside>
  );
}
