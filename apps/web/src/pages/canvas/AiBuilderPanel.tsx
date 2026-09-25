import { useRef } from 'react';
import { Sparkles, X } from 'lucide-react';
import { ProposalChanges } from './ProposalChanges';
import type { ProposalChange } from '../../utils/pipelineProposal';
import { MermaidPreview } from '../../components/MermaidPreview';
import type { AiGenerateResult } from '../../hooks/useAiGenerate';

// Right-hand "Build with AI" chat panel: prompt input, message history, and
// the pending proposal (mermaid preview + apply/discard/retry).
export function AiBuilderPanel({
  showAI, setShowAI, hasNodes,
  aiMessages, aiProposal, aiProposalStale, aiChanges, applyAI, discardProposal, aiLoading, runAI,
  aiPrompt, setAiPrompt, aiError, aiUndo, undoAI,
}: {
  showAI: boolean;
  setShowAI: (show: boolean) => void;
  hasNodes: boolean;
  aiMessages: Array<{ role: 'user' | 'assistant'; content: string }>;
  aiProposal: AiGenerateResult | null;
  aiProposalStale: boolean;
  aiChanges: ProposalChange[] | null;
  applyAI: () => void;
  discardProposal: () => void;
  aiLoading: boolean;
  runAI: () => void;
  aiPrompt: string;
  setAiPrompt: (prompt: string) => void;
  aiError: string | null;
  aiUndo: any | null;
  undoAI: () => void;
}) {
  const requestInput = useRef<HTMLInputElement>(null);
  const finishReview = (action: () => void) => {
    action();
    requestInput.current?.focus();
  };
  return (
    <aside
      className={`flex flex-col flex-none overflow-hidden border-l border-gray-200 dark:border-white/[0.08] bg-white dark:bg-[#0d1018] transition-[width] duration-200 ease-in-out ${
        showAI ? 'w-[390px]' : 'w-0'
      }`}
    >
      {showAI && (
        <>
          <div className="flex items-center gap-3 border-b border-gray-100 px-4 py-3 dark:border-white/[0.07]">
            <Sparkles size={16} className="text-brand-500 flex-none" />
            <b className="text-sm text-gray-900 dark:text-white/90">Build with AI</b>
            <button className="icon-button ml-auto h-7 w-7" onClick={() => setShowAI(false)} aria-label="Close AI panel"><X size={14} /></button>
          </div>
          <div className="flex-1 space-y-3 overflow-y-auto p-4">
            {aiMessages.map((m, i) => <div key={i} className={`rounded-xl p-3 text-xs ${m.role === 'user' ? 'ml-8 bg-brand-500/10 text-gray-800 dark:text-white/80' : 'mr-8 bg-gray-100 text-gray-600 dark:bg-white/[0.06] dark:text-white/60'}`}>{m.content}</div>)}
            {aiProposal && <div className="space-y-3 rounded-xl border border-brand-300/30 bg-brand-500/[0.05] p-3">
              <div role="status" aria-live="polite" className="text-xs font-semibold text-gray-900 dark:text-white/90">
                {aiProposal.status === 'ready'
                  ? `Proposed graph · ${aiProposal.definition?.nodes?.length ?? 0} nodes`
                  : aiProposal.status === 'needs_input' ? 'More information needed' : 'No proposal created'}
              </div>
              {aiProposal.status === 'ready' && aiProposal.mermaid && <div className="max-h-52 overflow-auto rounded-lg bg-white p-2 dark:bg-black/20"><MermaidPreview source={aiProposal.mermaid} /></div>}
              {aiProposal.status === 'ready' && aiProposal.definition?.execution?.engine && <div className="text-xs text-gray-500 dark:text-white/50">Engine: {aiProposal.definition.execution.engine}</div>}
              {aiProposal.questions.length > 0 && <div className="text-xs text-gray-700 dark:text-white/70">
                <div className="font-medium">Questions</div>
                <ul className="mt-1 list-disc space-y-1 pl-4">{aiProposal.questions.map((question, i) => <li key={i}>{question}</li>)}</ul>
              </div>}
              {aiProposal.assumptions.length > 0 && <div className="text-xs text-gray-500 dark:text-white/55">
                <div className="font-medium">Assumptions</div>
                <ul className="mt-1 list-disc space-y-1 pl-4">{aiProposal.assumptions.map((assumption, i) => <li key={i}>{assumption}</li>)}</ul>
              </div>}
              {aiProposal.warnings.map((warning, i) => <div key={i} className="text-xs text-amber-600 dark:text-amber-400">{warning}</div>)}
              {aiChanges && <ProposalChanges changes={aiChanges} />}
              {aiProposalStale && <p role="status" id="ai-proposal-stale" className="text-xs text-amber-700 dark:text-amber-300">This proposal is based on an older draft. Retry to regenerate before applying.</p>}
              <div className="flex gap-2">
                {aiProposal.status === 'ready' && aiProposal.definition && <button className="glass-btn-primary text-xs disabled:opacity-40" disabled={aiProposalStale} aria-describedby={aiProposalStale ? 'ai-changes-summary ai-proposal-stale' : 'ai-changes-summary'} onClick={() => finishReview(applyAI)}>Apply</button>}
                <button className="glass-btn-ghost text-xs" onClick={() => finishReview(discardProposal)}>Discard</button>
                <button className="glass-btn-ghost text-xs" disabled={aiLoading || !aiPrompt.trim()} onClick={runAI}>Retry</button>
              </div>
            </div>}
          </div>
          <div className="border-t border-gray-100 p-3 dark:border-white/[0.07]">
            <div className="flex items-center gap-2">
              <input ref={requestInput}
                className="glass-input flex-1 text-sm"
                placeholder={hasNodes ? 'Describe changes… e.g. Add a filter step before the Postgres sink' : 'Describe your pipeline… e.g. Sync Zendesk tickets to Postgres'}
                aria-label="AI pipeline request"
                value={aiPrompt} onChange={e => setAiPrompt(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && runAI()} autoFocus />
              <button className="glass-btn-primary text-xs flex-none"
                disabled={aiLoading || !aiPrompt.trim()} onClick={runAI}>
                {aiLoading ? '…' : hasNodes ? 'Refine' : 'Generate'}
              </button>
            </div>
            {aiError && <p className="border-t border-red-100 bg-red-50 px-4 py-2 text-[10px] text-red-600 dark:border-red-500/15 dark:bg-red-500/[0.06] dark:text-red-300">{aiError}</p>}
            {aiLoading && <div className="h-0.5 animate-pulse bg-brand-500" />}
            {aiUndo && <button className="mt-2 text-xs text-brand-500" onClick={() => finishReview(undoAI)}>Undo last apply</button>}
          </div>
        </>
      )}
    </aside>
  );
}
