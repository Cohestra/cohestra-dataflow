import {
  ArrowDownToLine, BadgeHelp, Braces, Database, FileJson, FileSpreadsheet,
  Filter, GitFork, Globe2, HardDrive, Merge, Sheet, Triangle, Webhook,
} from 'lucide-react';
import { Handle, Position } from 'reactflow';
import type { CatalogEntry } from '@dataflow/shared';
import { useCatalog } from '../../context/CatalogContext';

type LucideIcon = typeof Braces;
const EXACT_ICON: Record<string, LucideIcon> = {
  'sink.postgres': Database, 'sink.webhook': Webhook,
  'sink.records': HardDrive, 'transform.filter': Filter,
};
const PREFIX_ICON: Array<[string, LucideIcon]> = [
  ['zendesk.', BadgeHelp], ['gsheets.', Sheet], ['gdrive.', Triangle],
  ['excel.', FileSpreadsheet], ['http.', Globe2], ['transform.parse', FileJson],
];
const NODE_TYPE_ICON: Record<string, LucideIcon> = {
  source: Database, sink: ArrowDownToLine, fork: GitFork, merge: Merge,
};

function resolveIcon(activityType?: string, nodeType?: string): LucideIcon {
  if (activityType) {
    if (EXACT_ICON[activityType]) return EXACT_ICON[activityType];
    const match = PREFIX_ICON.find(([p]) => activityType.startsWith(p));
    if (match) return match[1];
  }
  return (nodeType && NODE_TYPE_ICON[nodeType]) || Braces;
}

export function ActivityIcon({ activityType, nodeType, size = 16 }: {
  activityType?: string; nodeType?: string; size?: number;
}) {
  const Icon = resolveIcon(activityType, nodeType);
  return <Icon size={size} strokeWidth={1.8} />;
}

export function FlowNode({ data }: { data: any }) {
  const { byType } = useCatalog();
  const entry: CatalogEntry = byType[data.activityType];
  const statusRing =
    data.status === 'failed'  ? 'ring-1 ring-rose-400/60'
    : data.status === 'success' ? 'ring-1 ring-emerald-400/50'
    : '';
  return (
    <div className={`relative min-w-[180px] overflow-hidden rounded-[14px] px-3.5 py-3 text-[13px]
      border bg-white border-gray-200 text-gray-900 shadow-xs
      dark:bg-[#11141d]/90 dark:border-white/10 dark:text-white/90 dark:shadow-[0_16px_35px_rgba(0,0,0,.28)]
      transition-all duration-200 hover:-translate-y-0.5 hover:shadow-md hover:border-gray-300
      dark:hover:border-white/18 backdrop-blur-xl ${statusRing}`}>
      <div className="absolute inset-y-0 left-0 w-[3px] rounded-l-[14px]" style={{ background: entry?.color ?? '#6965db' }} />
      <Handle type="target" position={Position.Left} />
      <div className="flex items-center gap-2.5">
        <span className="flex h-8 w-8 items-center justify-center rounded-[10px] border border-gray-100 bg-gray-50 dark:border-white/8 dark:bg-white/4.5" style={{ color: entry?.color }}>
          <ActivityIcon activityType={data.activityType} nodeType={data.nodeType} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="truncate font-semibold">{data.label || entry?.label || data.activityType}</div>
          <div className="truncate text-[10px] text-gray-400 dark:text-white/35">{entry?.label ?? data.activityType}</div>
        </div>
        {data.status && (
          <span className={`h-2 w-2 rounded-full ${
            data.status === 'failed'  ? 'bg-rose-400'
            : data.status === 'success' ? 'bg-emerald-400'
            : 'animate-pulse bg-amber-300'
          }`} title={data.status} />
        )}
      </div>
      {data.recordCount != null && (
        <div className="mt-2 text-[10px] text-gray-400 dark:text-white/35">{data.recordCount.toLocaleString()} records</div>
      )}
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

export const nodeTypes = { flowNode: FlowNode };
