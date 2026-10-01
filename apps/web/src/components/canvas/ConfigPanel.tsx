import { useEffect, useId, useState } from 'react';
import { Settings2, Trash2 } from 'lucide-react';
import type { Node } from 'reactflow';
import type { CatalogEntry, FieldSpec } from '@dataflow/shared';
import { useCatalog } from '../../context/CatalogContext';
import { api } from '../../api';
import { SheetPicker } from '../connectors/SheetPicker';
import { DrivePicker } from '../connectors/DrivePicker';
import { ExcelPicker } from '../connectors/ExcelPicker';
import { ZendeskPicker } from '../connectors/ZendeskPicker';

const PICKER_MAP: Record<string, React.ComponentType<{ value: Record<string, string>; onChange: (p: Record<string, string>) => void }>> = {
  gsheets: SheetPicker,
  gdrive: DrivePicker,
  excel: ExcelPicker,
  zendesk: ZendeskPicker,
};

function OAuthPickerField({ field, value, onChange }: {
  field: FieldSpec; value: Record<string, string>; onChange: (p: Record<string, string>) => void;
}) {
  const Comp = field.picker ? PICKER_MAP[field.picker] : null;
  return Comp ? <Comp value={value} onChange={onChange} /> : null;
}

function InstancePicker({ id, describedBy, provider, value, onChange }: {
  id: string; describedBy?: string; provider?: string; value: Record<string, string>; onChange: (p: Record<string, string>) => void;
}) {
  const [instances, setInstances] = useState<any[]>([]);
  useEffect(() => {
    api.listConnectors()
      .then((l: any[]) => setInstances(l.filter(i => !provider || i.provider === provider)))
      .catch(() => setInstances([]));
  }, [provider]);
  return (
    <select id={id} aria-describedby={describedBy} className="glass-select" value={value.connectionId ?? ''}
      onChange={e => onChange({ connectionId: e.target.value })}>
      <option value="">— select connection —</option>
      {instances.map(i => <option key={i.id} value={i.id}>{i.name ?? i.email ?? i.id} ({i.provider}{i.kind ? `, ${i.kind}` : ''})</option>)}
    </select>
  );
}

export function ConfigPanel({ node, onChange, onDelete }: {
  node: Node; onChange: (id: string, patch: any) => void; onDelete: (id: string) => void;
}) {
  const { byType } = useCatalog();
  const uid = useId();
  const entry: CatalogEntry = byType[node.data.activityType];
  if (!entry) return null;
  const cfg = node.data.config ?? {};
  const ing = node.data.ingestion ?? { mode: 'incremental' };
  const visible = (field: FieldSpec) => {
    if (!field.visibleWhen) return true;
    const controller = entry.fields.find(f => f.key === field.visibleWhen?.key);
    return (cfg[field.visibleWhen.key] ?? controller?.options?.[0]) === field.visibleWhen.equals;
  };
  return (
    <div>
      <div className="mb-5 flex items-center gap-3">
        <span className="flex h-10 w-10 items-center justify-center rounded-xl border border-gray-100 bg-gray-50 dark:border-white/[0.08] dark:bg-white/[0.05]" style={{ color: entry.color }}>
          <Settings2 size={18} />
        </span>
        <div>
          <h3 className="m-0 text-sm font-semibold text-gray-900 dark:text-white/90">{entry.label}</h3>
          <p className="text-[11px] text-gray-400 dark:text-white/35">{node.data.activityType}</p>
        </div>
      </div>
      <label className="glass-label">Label
        <input className="glass-input" value={node.data.label ?? ''}
          onChange={e => onChange(node.id, { label: e.target.value })} />
      </label>
      {entry.fields.filter(visible).map(f => {
        // Every control is named by its visible field title; help text is its description.
        const id = `${uid}-${f.key}`;
        const labelId = `${id}-label`;
        const helpId = f.help ? `${id}-help` : undefined;
        const set = (value: unknown) => onChange(node.id, { config: { ...cfg, [f.key]: value } });
        return (
          <div key={f.key} className="glass-label" role={f.type === 'oauth-picker' ? 'group' : undefined}
            aria-labelledby={f.type === 'oauth-picker' ? labelId : undefined} aria-describedby={f.type === 'oauth-picker' ? helpId : undefined}>
            {f.type === 'oauth-picker' ? <span id={labelId}>{f.label}</span> : <label id={labelId} htmlFor={id}>{f.label}</label>}
            {f.type === 'oauth-picker' ? (
              <OAuthPickerField field={f} value={cfg} onChange={p => onChange(node.id, { config: { ...cfg, ...p } })} />
            ) : f.type === 'instance-picker' ? (
              <InstancePicker id={id} describedBy={helpId} provider={f.provider} value={cfg} onChange={p => onChange(node.id, { config: { ...cfg, ...p } })} />
            ) : f.type === 'checkbox' ? (
              <input id={id} aria-describedby={helpId} type="checkbox" checked={!!cfg[f.key]} onChange={e => set(e.target.checked)} />
            ) : f.type === 'select' ? (
              <select id={id} aria-describedby={helpId} className="glass-select" value={cfg[f.key] ?? f.options?.[0]} onChange={e => set(e.target.value)}>
                {f.options?.map(o => <option key={o}>{o}</option>)}
              </select>
            ) : f.type === 'textarea' ? (
              <textarea id={id} aria-describedby={helpId} className="glass-input h-16 font-mono text-[11px]"
                placeholder={f.placeholder} value={cfg[f.key] ?? ''} onChange={e => set(e.target.value)} />
            ) : (
              <input id={id} aria-describedby={helpId} className="glass-input" placeholder={f.placeholder} value={cfg[f.key] ?? ''}
                onChange={e => set(e.target.value)} />
            )}
            {f.help && <span id={helpId} className="text-[10px] opacity-60">{f.help}</span>}
          </div>
        );
      })}
      {entry.supportsIngestion && cfg.syncMode !== 'cdc' && (
        <fieldset className="my-4 rounded-[14px] border border-gray-200 dark:border-white/[0.08] bg-gray-50 dark:bg-black/10 p-3">
          <legend className="text-[11px] text-gray-500 dark:opacity-70 px-1">Ingestion mode</legend>
          <select className="glass-select" aria-label="Ingestion mode" value={ing.mode}
            onChange={e => onChange(node.id, { ingestion: { ...ing, mode: e.target.value } })}>
            <option value="incremental">Incremental (cursor)</option>
            <option value="backfill">Historical backfill → then incremental</option>
          </select>
          {ing.mode === 'backfill' && (<>
            <label className="glass-label">Backfill start (ISO)
              <input className="glass-input" placeholder="2024-01-01T00:00:00Z" value={ing.backfillStart ?? ''}
                onChange={e => onChange(node.id, { ingestion: { ...ing, backfillStart: e.target.value } })} />
            </label>
            <label className="glass-label">Page size
              <input className="glass-input" type="number" value={ing.pageSize ?? 100}
                onChange={e => onChange(node.id, { ingestion: { ...ing, pageSize: +e.target.value } })} />
            </label>
          </>)}
        </fieldset>
      )}
      <button className="glass-btn-danger mt-4 w-full" onClick={() => onDelete(node.id)}>
        <Trash2 size={15} /> Delete node
      </button>
    </div>
  );
}
