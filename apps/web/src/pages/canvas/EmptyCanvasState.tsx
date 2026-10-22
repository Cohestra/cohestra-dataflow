import { Database, Sparkles } from 'lucide-react';

// Centered CTA shown before any nodes exist on the canvas.
export function EmptyCanvasState({ onAddSource, onGenerateAI }: { onAddSource: () => void; onGenerateAI: () => void }) {
  return (
    <div className="absolute left-1/2 top-1/2 z-10 -translate-x-1/2 -translate-y-1/2 flex flex-col items-center gap-4 text-center pointer-events-none">
      <p className="text-sm font-medium text-gray-400 dark:text-white/30">Start building your pipeline</p>
      <div className="flex gap-2 pointer-events-auto">
        <button
          className="flex items-center gap-2 rounded-2xl border border-gray-200 dark:border-white/9 bg-white dark:bg-white/5 px-4 py-2.5 text-sm text-gray-600 dark:text-white/60 shadow-xs hover:bg-gray-50 hover:border-gray-300 dark:hover:bg-white/8 transition-all"
          onClick={onAddSource}>
          <Database size={15} className="text-emerald-500" /> Add a source
        </button>
        <button
          className="flex items-center gap-2 rounded-2xl border border-brand-300/30 bg-brand-500/10 text-brand-600 dark:text-brand-300 px-4 py-2.5 text-sm shadow-xs hover:bg-brand-500/15 transition-all"
          onClick={onGenerateAI}>
          <Sparkles size={15} /> Generate with AI
        </button>
      </div>
    </div>
  );
}
