import { Plus, X } from 'lucide-react';
import type { CatalogEntry } from '@dataflow/shared';
import type { Node } from 'reactflow';

export interface ContextAddState { source: string; x: number; y: number }

// Floating "Add next step" popup shown after dragging a connection out to
// empty canvas space. Small and purely presentational — the positioning math
// (finishConnection) and the node/edge creation (addNode) stay in the page
// since they need the canvas bounding box and node-id counter.
export function ContextAddMenu({
  contextAdd, setContextAdd, nodes, catalog, addNode,
}: {
  contextAdd: ContextAddState;
  setContextAdd: (value: ContextAddState | null) => void;
  nodes: Node[];
  catalog: CatalogEntry[];
  addNode: (entry: CatalogEntry, sourceId?: string) => void;
}) {
  const sourceIsSink = nodes.find(node => node.id === contextAdd.source)?.data.nodeType === 'sink';
  return (
    <div className="absolute z-40 w-72 rounded-2xl border border-gray-200 bg-white p-2 shadow-2xl dark:border-white/10 dark:bg-[#12151e]"
      style={{ left: contextAdd.x, top: contextAdd.y }}>
      <div className="flex items-center justify-between px-2 py-1.5">
        <div>
          <p className="text-xs font-semibold text-gray-900 dark:text-white/90">Add next step</p>
          <p className="text-[10px] text-gray-400 dark:text-white/35">Valid transforms, branches, and destinations</p>
        </div>
        <button aria-label="Close" className="icon-button h-7 w-7" onClick={() => setContextAdd(null)}><X size={13} /></button>
      </div>
      <div className="max-h-48 overflow-auto pt-1">
        {!sourceIsSink && catalog.filter(entry => entry.nodeType !== 'source').map(entry => (
          <button key={entry.activityType} onClick={() => addNode(entry, contextAdd.source)}
            className="flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2 text-left hover:bg-gray-50 dark:hover:bg-white/6">
            <span className="h-7 w-1 rounded-full" style={{ background: entry.color }} />
            <span className="min-w-0 flex-1">
              <span className="block text-xs font-medium text-gray-700 dark:text-white/75">{entry.label}</span>
              <span className="block text-[10px] capitalize text-gray-400 dark:text-white/30">{entry.nodeType}</span>
            </span>
            <Plus size={13} className="text-gray-300 dark:text-white/25" />
          </button>
        ))}
        {sourceIsSink && (
          <p className="px-2.5 py-4 text-xs text-gray-400 dark:text-white/35">Destinations end a pipeline branch.</p>
        )}
      </div>
    </div>
  );
}
