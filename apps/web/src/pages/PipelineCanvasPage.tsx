import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useSearchParams } from 'react-router';
import {
  addEdge, useNodesState, useEdgesState,
  type Node, type Connection, type ReactFlowInstance,
} from 'reactflow';
import { definitionToMermaid, mermaidToDefinition, type PipelineDefinition } from '@dataflow/shared';
import { useCatalog } from '../context/CatalogContext';
import { useAuth } from '../context/AuthContext';
import { useTheme } from '../context/ThemeContext';
import { useFeatures } from '../context/FeatureContext';
import { api } from '../api';
import { nodeTypes } from '../components/canvas/FlowNode';
import { ExecutionMonitor } from '../components/canvas/ExecutionMonitor';
import { useAiGenerate, type AiGenerateResult } from '../hooks/useAiGenerate';
import { useApiQuery } from '../hooks/useApiQuery';
import { definitionToFlow, flowToDefinition, applyGraphEdit, pipelineFingerprint } from '../utils/pipelineConvert';
import { validatePipeline } from '../utils/validatePipeline';
import { deriveStage, displayEnvironment, type Stage } from '../utils/pipelineStage';
import { NodePalette, MOBILE_RAIL_CLEARANCE, type CatId } from './canvas/NodePalette';
import { ContextAddMenu, type ContextAddState } from './canvas/ContextAddMenu';
import { PipelineFlowCanvas } from './canvas/PipelineFlowCanvas';
import { PipelineHeaderBar } from './canvas/PipelineHeaderBar';
import { PipelineActionBar } from './canvas/PipelineActionBar';
import { WorkspaceSidePanel, type WorkspacePanel, type PipelinePolicy } from './canvas/WorkspaceSidePanel';
import { AiBuilderPanel } from './canvas/AiBuilderPanel';
import { InspectorPanel } from './canvas/InspectorPanel';
import { OutputDrawer, type BottomTab } from './canvas/OutputDrawer';
import { EmptyCanvasState } from './canvas/EmptyCanvasState';

let nid = 0;

export default function PipelineCanvasPage() {
  const { catalog, byType } = useCatalog();
  const location = useLocation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { dark, toggle: toggleTheme } = useTheme();
  const { user } = useAuth();
  const { features } = useFeatures();
  const hydrated = useRef<string | null>(null);
  const canvasRef = useRef<HTMLDivElement>(null);
  const connectSource = useRef<string | null>(null);
  const connected = useRef(false);
  const fitPending = useRef(false);
  const resizeHandlers = useRef<{ move?: (event: PointerEvent) => void; stop?: () => void }>({});
  const cleanAfterHydration = useRef(false);

  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const [selected, setSelected] = useState<Node | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<any | null>(null);
  const [flow, setFlow] = useState<ReactFlowInstance | null>(null);

  const [baseDefinition, setBaseDefinition] = useState<Partial<PipelineDefinition>>({});
  const hydratedPolicy = useRef<PipelinePolicy | null>(null);
  const [name, setName] = useState('My pipeline');
  const [pipelineKey, setPipelineKey] = useState('');
  const [trigger, setTrigger] = useState<any>({ type: 'manual' });
  const [execution, setExecution] = useState<any>(undefined);
  const [policy, setPolicy] = useState<PipelinePolicy>({ owner: '', domain: '', tags: '', freshnessMinutes: '', maxFailureRatePercent: '', maxDurationSeconds: '', notificationConnectionId: '', minimumSeverity: 'critical' });
  const [savedRowId, setSavedRowId] = useState<string | null>(null);
  const [pipelineStage, setPipelineStage] = useState<Stage>('draft');
  const [executionId, setExecutionId] = useState<string | null>(null);
  const [msg, setMsg] = useState('');
  const [savedFingerprint, setSavedFingerprint] = useState<string | null>(null);

  const graphValidationErrors = useMemo(() => validatePipeline(
    nodes.map(node => ({ id: node.id, type: node.data.nodeType ?? node.type ?? '', label: node.data.label })),
    edges.map(edge => ({ id: edge.id, source: edge.source, target: edge.target })),
  ), [nodes, edges]);
  const graphReady = graphValidationErrors.length === 0;

  const [activeCat, setActiveCat] = useState<CatId | null>(null);
  const [workspacePanel, setWorkspacePanel] = useState<WorkspacePanel>(null);
  const [showLifecycle, setShowLifecycle] = useState(false);
  const [showMermaid, setShowMermaid] = useState(false);
  const [mermaidDraft, setMermaidDraft] = useState('');
  const [mermaidValid, setMermaidValid] = useState(false);
  const [showAI, setShowAI] = useState(false);
  const [aiPrompt, setAiPrompt] = useState('');
  const [aiMessages, setAiMessages] = useState<Array<{ role: 'user' | 'assistant'; content: string }>>([]);
  const [aiProposal, setAiProposal] = useState<AiGenerateResult | null>(null);
  const [aiUndo, setAiUndo] = useState<PipelineDefinition | null>(null);
  const [aiProposalFingerprint, setAiProposalFingerprint] = useState<string | null>(null);
  const { generate: aiGenerate, refine: aiRefine, loading: aiLoading, error: aiError } = useAiGenerate();

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [bottomTab, setBottomTab] = useState<BottomTab>('runs');
  const [drawerHeight, setDrawerHeight] = useState(260);
  const [drawerExpanded, setDrawerExpanded] = useState(false);
  const [recentRuns, setRecentRuns] = useState<any[]>([]);
  const [runsLoading, setRunsLoading] = useState(false);
  const [selectedRun, setSelectedRun] = useState<any | null>(null);
  const [runDetail, setRunDetail] = useState<any | null>(null);
  const [contextAdd, setContextAdd] = useState<ContextAddState | null>(null);
  const [inviteEmail, setInviteEmail] = useState('');
  const [inviteBusy, setInviteBusy] = useState(false);
  const [inviteMsg, setInviteMsg] = useState('');

  // ── Typed query/cache layer (apps/web/src/hooks/useApiQuery.ts) for the
  // page's read-only, key-driven fetches. The pipeline-by-id hydration below
  // stays a manual effect: it drives multi-field hydration + a `hydrated`
  // ref guard rather than rendering a single resource, so it doesn't fit the
  // data/error/loading shape.
  const pipelinesQuery = useApiQuery(() => api.listAllPipelines(), []);
  const upstreamPipelines = useMemo(() => {
    const byKey = new Map<string, { pipeline_key: string; name: string }>();
    (pipelinesQuery.data ?? []).forEach((row: any) => { if (!byKey.has(row.pipeline_key)) byKey.set(row.pipeline_key, row); });
    return [...byKey.values()];
  }, [pipelinesQuery.data]);

  const lineageQuery = useApiQuery(() => api.workspaceLineage('test'), []);
  const workspaceAssets = useMemo(() => {
    const graph = lineageQuery.data as any;
    if (!graph) return [];
    const produced = new Set((graph.edges ?? []).filter((edge: any) =>
      String(edge.source).startsWith('pipeline:') && String(edge.target).startsWith('asset:')).map((edge: any) => edge.target));
    return (graph.nodes ?? []).filter((node: any) => node.kind === 'asset' && produced.has(node.id))
      .map((node: any) => ({ urn: node.asset.urn, name: node.asset.name, layer: node.asset.layer }));
  }, [lineageQuery.data]);

  const connectorsQuery = useApiQuery(() => (workspacePanel ? api.listConnectors() : Promise.resolve([])), [workspacePanel]);
  const connectorInstances = connectorsQuery.data ?? [];
  const settingsQuery = useApiQuery(
    () => (workspacePanel === 'settings' ? Promise.all([api.listMembers(), api.getUsage()]) : Promise.resolve([[], null] as [any[], any])),
    [workspacePanel],
  );
  const members = settingsQuery.data?.[0] ?? [];
  const usage = settingsQuery.data?.[1] ?? null;

  useEffect(() => {
    const err = pipelinesQuery.error ?? lineageQuery.error;
    if (err) setMsg(`Load failed: ${err}`);
  }, [pipelinesQuery.error, lineageQuery.error]);

  const hydrateFromDefinition = useCallback((def: any, message: string) => {
    const snapshot = structuredClone(def);
    setBaseDefinition(snapshot);
    const { nodes: ns, edges: es } = definitionToFlow(snapshot, byType);
    fitPending.current = true;
    setNodes(ns); setEdges(es);
    setName(snapshot.name ?? 'My pipeline');
    setPipelineKey(snapshot.id ?? '');
    setTrigger(snapshot.trigger ?? { type: 'manual' });
    setExecution(snapshot.execution);
    const nextPolicy: PipelinePolicy = {
      owner: def.metadata?.owner ?? '', domain: def.metadata?.domain ?? '', tags: (def.metadata?.tags ?? []).join(', '),
      freshnessMinutes: def.slo?.freshnessMinutes?.toString() ?? '',
      maxFailureRatePercent: def.slo?.maxFailureRatePercent?.toString() ?? '',
      maxDurationSeconds: def.slo?.maxDurationMs !== undefined ? String(def.slo.maxDurationMs / 1000) : '',
      notificationConnectionId: def.notifications?.connectionId ?? '',
      minimumSeverity: def.notifications?.minimumSeverity ?? 'critical',
    };
    hydratedPolicy.current = nextPolicy;
    setPolicy(nextPolicy);
    setMsg(message);
  }, [byType]);

  useEffect(() => {
    if (searchParams.get('ai') === '1') setShowAI(true);
  }, [searchParams]);

  useEffect(() => {
    let cancelled = false;
    const stateDef = (location.state as any)?.definition;
    const pipelineId = (location.state as any)?.pipelineId ?? new URLSearchParams(location.search).get('pipeline');
    const openBackfill = (location.state as any)?.openBackfill === true || new URLSearchParams(location.search).get('backfill') === '1';
    const hydrationKey = pipelineId ?? (stateDef ? 'generated' : null);
    if (!hydrationKey || hydrated.current === hydrationKey) return;
    setAiUndo(null); setAiProposal(null);

    if (stateDef) {
      hydrated.current = hydrationKey;
      setSavedRowId(null); setSavedFingerprint(null);
      hydrateFromDefinition(stateDef, 'Loaded from AI builder — review and Save');
      return;
    }
    if (pipelineId) {
      setSavedRowId(null); setSavedFingerprint(null); setSelected(null); setSelectedEdge(null);
      setNodes([]); setEdges([]); setMsg('Loading pipeline…');
      api.getPipeline(pipelineId).then((row: any) => {
        if (cancelled) return;
        hydrated.current = hydrationKey;
        cleanAfterHydration.current = true;
        hydrateFromDefinition(row.definition, `Loaded v${row.version}`);
        setSavedRowId(row.id);
        setPipelineStage(deriveStage(row.status, row.environment));
        if (openBackfill) { setShowLifecycle(true); openDrawer('lifecycle'); }
      }).catch((e: any) => { if (!cancelled) setMsg(`Load failed: ${e.message}`); });
    }
    return () => { cancelled = true; };
  }, [location.search, location.state, byType]);

  useEffect(() => {
    if (!activeCat && !workspacePanel) return;
    const close = (event: PointerEvent) => {
      if (!(event.target as Element).closest('[data-canvas-sidebar]')) {
        setActiveCat(null); setWorkspacePanel(null);
      }
    };
    document.addEventListener('pointerdown', close);
    return () => document.removeEventListener('pointerdown', close);
  }, [activeCat, workspacePanel]);

  useEffect(() => {
    if (!flow || !fitPending.current || !nodes.length) return;
    fitPending.current = false;
    requestAnimationFrame(() => flow.fitView({ padding: .2, duration: 300 }));
  }, [flow, nodes]);

  useEffect(() => () => resizeHandlers.current.stop?.(), []);

  const onConnect = useCallback((c: Connection) => {
    connected.current = true;
    setEdges(eds => addEdge({ ...c, id: `e${Date.now()}` }, eds));
  }, []);

  const addNode = (entry: any, sourceId?: string) => {
    const id = `n${++nid}-${Date.now() % 10000}`;
    setNodes(ns => {
      const source = sourceId ? ns.find(n => n.id === sourceId) : null;
      return [...ns, {
        id, type: 'flowNode',
        position: source
          ? { x: source.position.x + 260, y: source.position.y }
          : { x: 160 + ns.length * 44, y: 120 + (ns.length % 5) * 90 },
        data: { activityType: entry.activityType, nodeType: entry.nodeType, config: {}, label: entry.label },
      }];
    });
    if (sourceId) setEdges(es => addEdge({ id: `e${Date.now()}`, source: sourceId, target: id }, es));
    setContextAdd(null);
  };

  const finishConnection = (event: MouseEvent | TouchEvent) => {
    if (connected.current || !connectSource.current || !canvasRef.current) {
      connected.current = false; connectSource.current = null; return;
    }
    const point = 'changedTouches' in event ? event.changedTouches[0] : event;
    const bounds = canvasRef.current.getBoundingClientRect();
    setContextAdd({
      source: connectSource.current,
      x: Math.max(8, Math.min(point.clientX - bounds.left, bounds.width - 300)),
      y: Math.max(8, Math.min(point.clientY - bounds.top, bounds.height - 240)),
    });
    connectSource.current = null;
  };

  const patchNode = (id: string, patch: any) =>
    setNodes(ns => ns.map(n => n.id === id ? { ...n, data: { ...n.data, ...patch } } : n));

  const patchEdgeCondition = (id: string, condition: string) =>
    setEdges(es => es.map(e => e.id === id ? {
      ...e, data: { ...e.data, condition },
      label: condition || undefined, animated: !!condition,
      style: condition ? { stroke: '#f5b342' } : undefined,
      labelStyle: condition ? { fill: '#f5b342', fontSize: 10 } : undefined,
    } : e));

  const deleteNode = (id: string) => {
    setNodes(ns => ns.filter(n => n.id !== id));
    setEdges(es => es.filter(e => e.source !== id && e.target !== id));
    setSelected(null);
  };

  const buildDefinition = () => flowToDefinition(nodes, edges, {
    name, trigger, pipelineKey, execution,
    metadata: policy.owner === hydratedPolicy.current?.owner && policy.domain === hydratedPolicy.current?.domain && policy.tags === hydratedPolicy.current?.tags ? baseDefinition.metadata : {
      ...baseDefinition.metadata,
      owner: policy.owner.trim() || undefined, domain: policy.domain.trim() || undefined,
      tags: policy.tags.split(',').map(tag => tag.trim()).filter(Boolean),
    },
    slo: policy.freshnessMinutes === hydratedPolicy.current?.freshnessMinutes && policy.maxFailureRatePercent === hydratedPolicy.current?.maxFailureRatePercent && policy.maxDurationSeconds === hydratedPolicy.current?.maxDurationSeconds ? baseDefinition.slo : {
      ...baseDefinition.slo,
      freshnessMinutes: policy.freshnessMinutes ? Number(policy.freshnessMinutes) : undefined,
      maxFailureRatePercent: policy.maxFailureRatePercent ? Number(policy.maxFailureRatePercent) : undefined,
      maxDurationMs: policy.maxDurationSeconds ? Number(policy.maxDurationSeconds) * 1000 : undefined,
    },
    notifications: policy.notificationConnectionId === hydratedPolicy.current?.notificationConnectionId && policy.minimumSeverity === hydratedPolicy.current?.minimumSeverity ? baseDefinition.notifications : policy.notificationConnectionId ? {
      connectionId: policy.notificationConnectionId,
      minimumSeverity: policy.minimumSeverity as 'warning' | 'critical',
    } : undefined,
  }, baseDefinition);

  const definitionFingerprint = pipelineFingerprint(buildDefinition());
  const aiProposalStale = aiProposal !== null && aiProposalFingerprint !== definitionFingerprint;
  const hasUnsavedChanges = savedRowId !== null && savedFingerprint !== definitionFingerprint;

  useEffect(() => {
    if (!cleanAfterHydration.current) return;
    cleanAfterHydration.current = false;
    setSavedFingerprint(definitionFingerprint);
  }, [definitionFingerprint]);

  const validate = () => {
    const def = buildDefinition();
    const errs = validatePipeline(
      def.nodes.map(n => ({ id: n.id, type: n.type, label: n.label })),
      def.edges,
    );
    if (errs.length) { setMsg(errs[0].message); return false; }
    return true;
  };

  const save = async () => {
    if (!validate()) return;
    const definition = buildDefinition();
    try {
      const r = await api.savePipeline(definition);
      setSavedRowId(r.rowId);
      setPipelineKey(r.pipelineKey);
      setSavedFingerprint(pipelineFingerprint({ ...definition, id: r.pipelineKey }));
      setPipelineStage(deriveStage('inactive', 'test'));
      setMsg(`Saved v${r.version}`);
    } catch (e: any) { setMsg(`Save failed: ${e.message}`); }
  };

  const activate = async () => {
    if (!savedRowId) return setMsg('Save first');
    if (hasUnsavedChanges) return setMsg('Save changes before activating the pipeline');
    const r = await api.activate(savedRowId);
    setPipelineStage(deriveStage('active', r.environment));
    setMsg(`Activated in ${displayEnvironment(r.environment)}`);
  };

  const promote = async () => {
    if (!savedRowId) return setMsg('Save first');
    if (hasUnsavedChanges) return setMsg('Save changes before promoting the pipeline');
    try {
      const r = await api.promote(savedRowId);
      setPipelineStage('production');
      setMsg(`Promoted to production · v${r.version}`);
    } catch (e: any) {
      if (String(e.message).includes('breaking data contract') &&
          window.confirm(`Breaking contract detected. Promote anyway?\n\n${e.message}`)) {
        try {
          const r = await api.promote(savedRowId, true);
          setPipelineStage('production'); setMsg(`Promoted with contract override · v${r.version}`);
        } catch (override: any) { setMsg(`Promote failed: ${override.message}`); }
      } else setMsg(`Promote failed: ${e.message}`);
    }
  };

  const run = async () => {
    if (!savedRowId) return setMsg('Save first');
    if (hasUnsavedChanges) return setMsg('Save changes before running the pipeline');
    if (!validate()) return;
    try {
      const r = await api.run(savedRowId);
      setExecutionId(r.executionId);
      setMsg('Running…');
    } catch (e: any) { setMsg(`Run failed: ${e.message}`); }
  };

  const onNodeStatus = (results: Record<string, any>) =>
    setNodes(ns => ns.map(n => results[n.id]
      ? { ...n, data: { ...n.data, status: results[n.id].status, recordCount: results[n.id].meta?.recordCount } } : n));

  const openMermaid = () => {
    const def = buildDefinition();
    setMermaidDraft(definitionToMermaid(def.nodes as any, def.edges as any));
    setShowMermaid(true); setSelected(null); setSelectedEdge(null);
  };

  const applyMermaid = () => {
    if (!mermaidValid) return setMsg('Fix Mermaid validation errors before applying');
    const { nodes: parsed, edges: pEdges, warnings } = mermaidToDefinition(mermaidDraft, catalog);
    const previous = buildDefinition();
    const changedTypes = parsed.filter(node => previous.nodes.some(old => old.id === node.id && (old.activityType !== node.activityType || old.type !== node.type)));
    const removed = previous.nodes.filter(old => !parsed.some(node => node.id === old.id));
    if ((changedTypes.length || removed.length) && !window.confirm(
      `This edit removes settings and bindings for: ${[...changedTypes, ...removed].map(node => node.id).join(', ')}. Review these nodes before saving. Apply structural changes?`,
    )) return;
    const next = definitionToFlow(applyGraphEdit(previous, { nodes: parsed, edges: pEdges }, 'mermaid'), byType);
    setNodes(next.nodes); setEdges(next.edges); setSelected(null); setSelectedEdge(null);
    setMsg(warnings.length ? `Applied Mermaid · ${warnings.length} warning(s)` : 'Applied Mermaid');
  };

  const runAI = async () => {
    if (aiLoading || !aiPrompt.trim()) return;
    const hasExisting = nodes.length > 0;
    const currentDefinition = buildDefinition();
    const currentMermaid = hasExisting
      ? definitionToMermaid(currentDefinition.nodes, currentDefinition.edges)
      : '';
    setMsg(hasExisting ? 'Refining pipeline…' : 'Generating pipeline…');

    const result = hasExisting
      ? await aiRefine(currentDefinition, aiPrompt, currentMermaid, aiMessages)
      : await aiGenerate(aiPrompt, currentMermaid, aiMessages);

    if (result) {
      setAiProposal(structuredClone(result));
      setAiProposalFingerprint(pipelineFingerprint(currentDefinition));
      const responseSummary = [
        result.status === 'ready' ? 'Proposal ready' : result.status === 'needs_input' ? 'I need more information.' : 'I could not create a safe proposal.',
        ...result.questions.map(question => `Question: ${question}`),
        ...result.assumptions.map(assumption => `Assumption: ${assumption}`),
        ...result.warnings.map(warning => `Warning: ${warning}`),
      ].join('\n');
      setAiMessages(m => [...m, { role: 'user', content: aiPrompt }, { role: 'assistant', content: responseSummary }]);
      setMsg(result.status === 'ready'
        ? 'AI proposal ready — review before applying'
        : result.status === 'needs_input' ? 'AI needs more information' : 'AI could not create a safe proposal');
    }
  };

  const applyAI = () => {
    if (!aiProposal || aiProposal.status !== 'ready' || !aiProposal.definition) return;
    if (aiProposalStale) return setMsg('This proposal is based on an older draft. Retry to regenerate before applying.');
    const previous = buildDefinition();
    const proposal = aiProposal.definition;
    const next = applyGraphEdit(previous, proposal, 'ai');
    if (proposal.execution) next.execution = structuredClone(proposal.execution);
    if (!nodes.length) {
      next.name = proposal.suggestedName ?? proposal.name ?? name;
      next.trigger = structuredClone(proposal.trigger ?? previous.trigger);
    }
    setAiUndo(structuredClone(previous));
    hydrateFromDefinition(next, 'AI proposal applied');
    setSelected(null); setSelectedEdge(null);
    setAiProposal(null); setAiPrompt('');
  };

  const undoAI = () => {
    if (!aiUndo) return;
    hydrateFromDefinition(aiUndo, 'AI change undone');
    setSelected(null); setSelectedEdge(null); setAiUndo(null);
  };

  const openDrawer = async (tab: BottomTab = 'runs') => {
    setBottomTab(tab); setDrawerOpen(true);
    if (tab === 'runs') {
      setRunsLoading(true);
      try { setRecentRuns(await api.listExecutions(savedRowId ? { pipeline: savedRowId } : {})); }
      catch { /* ignore */ }
      finally { setRunsLoading(false); }
    }
  };

  const selectRun = async (row: any) => {
    setSelectedRun(row); setBottomTab('logs'); setRunDetail(null);
    try { setRunDetail(await api.getExecution(row.id)); }
    catch (e: any) { setRunDetail({ error: e.message }); }
  };

  const startResize = (event: React.PointerEvent) => {
    event.preventDefault();
    resizeHandlers.current.stop?.();
    const startY = event.clientY;
    const startHeight = drawerExpanded ? window.innerHeight * .52 : drawerHeight;
    setDrawerExpanded(false);
    const move = (e: PointerEvent) => setDrawerHeight(Math.max(150,
      Math.min(window.innerHeight * .52, startHeight + startY - e.clientY)));
    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
      resizeHandlers.current = {};
    };
    resizeHandlers.current = { move, stop };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
  };

  const inviteMember = async (event: React.FormEvent) => {
    event.preventDefault(); setInviteBusy(true); setInviteMsg('');
    try {
      await api.invite({ email: inviteEmail, role: 'member' });
      setInviteMsg('Invitation sent'); setInviteEmail('');
    } catch (e: any) { setInviteMsg(e.message); }
    finally { setInviteBusy(false); }
  };

  const rightPanelOpen = Boolean(selected || selectedEdge || showMermaid);
  const drawerOffset = drawerExpanded ? 'calc(52vh + 24px)' : drawerHeight + 24;
  const executionOffset = drawerExpanded ? 'calc(52vh + 28px)' : drawerHeight + 28;
  const selectedNode = selected ? (nodes.find(n => n.id === selected.id) ?? selected) : null;

  return (
    <div ref={canvasRef} className="flex h-screen overflow-hidden bg-[#f5f5f5] dark:bg-[#0d0f17]">
      {/* ── Canvas area (flex-1, shrinks when AI panel opens) ── */}
      <div className="relative flex-1 min-w-0 overflow-hidden">
        <PipelineFlowCanvas
          nodes={nodes} edges={edges} nodeTypes={nodeTypes}
          onInit={setFlow}
          onNodesChange={onNodesChange} onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onConnectStart={(_, params) => { connectSource.current = params.nodeId; connected.current = false; }}
          onConnectEnd={finishConnection}
          onNodeClick={(_, n) => { setSelected(n); setSelectedEdge(null); setShowMermaid(false); }}
          onEdgeClick={(_, ed) => { setSelectedEdge(ed); setSelected(null); setShowMermaid(false); }}
          onPaneClick={() => { setActiveCat(null); setWorkspacePanel(null); setShowLifecycle(false); setContextAdd(null); }}
          dark={dark} byType={byType} drawerOpen={drawerOpen} drawerOffset={drawerOffset}
        />

        {contextAdd && (
          <ContextAddMenu contextAdd={contextAdd} setContextAdd={setContextAdd} nodes={nodes} catalog={catalog} addNode={addNode} />
        )}

        <NodePalette
          catalog={catalog} activeCat={activeCat} setActiveCat={setActiveCat} addNode={addNode}
          showAI={showAI} setShowAI={setShowAI} openMermaid={openMermaid}
          workspacePanel={workspacePanel} setWorkspacePanel={setWorkspacePanel}
          drawerOpen={drawerOpen} bottomTab={bottomTab} openDrawer={openDrawer} setDrawerOpen={setDrawerOpen}
          dark={dark} toggleTheme={toggleTheme} navigate={navigate}
        />

        <WorkspaceSidePanel
          workspacePanel={workspacePanel} setWorkspacePanel={setWorkspacePanel}
          catalog={catalog} connectorInstances={connectorInstances}
          connectorsError={connectorsQuery.error} refreshConnectors={connectorsQuery.refresh} addNode={addNode}
          policy={policy} setPolicy={setPolicy} user={user} usage={usage} members={members}
          settingsError={settingsQuery.error} refreshSettings={settingsQuery.refresh}
          inviteEmail={inviteEmail} setInviteEmail={setInviteEmail} inviteBusy={inviteBusy}
          inviteMsg={inviteMsg} inviteMember={inviteMember}
        />

        <PipelineHeaderBar
          leftOffset={workspacePanel || activeCat ? 400 : 90}
          name={name} setName={setName}
          pipelineStage={pipelineStage} showLifecycle={showLifecycle} setShowLifecycle={setShowLifecycle}
          isOwner={user?.role === 'owner'} activate={activate} promote={promote}
          trigger={trigger} setTrigger={setTrigger} pipelineKey={pipelineKey}
          upstreamPipelines={upstreamPipelines} workspaceAssets={workspaceAssets}
        />

        <PipelineActionBar
          msg={msg} graphReady={graphReady} firstValidationError={graphValidationErrors[0]?.message}
          hasUnsavedChanges={hasUnsavedChanges}
          execution={execution} setExecution={setExecution} features={features}
          savedRowId={savedRowId} save={save} activate={activate} run={run}
        />
      </div>{/* end canvas area */}

      <AiBuilderPanel
        showAI={showAI} setShowAI={setShowAI} hasNodes={nodes.length > 0}
        aiMessages={aiMessages} aiProposal={aiProposal} aiProposalStale={aiProposalStale} applyAI={applyAI}
        discardProposal={() => setAiProposal(null)} aiLoading={aiLoading} runAI={runAI}
        aiPrompt={aiPrompt} setAiPrompt={setAiPrompt} aiError={aiError} aiUndo={aiUndo} undoAI={undoAI}
      />

      <InspectorPanel
        open={rightPanelOpen} showMermaid={showMermaid} selected={selectedNode} selectedEdge={selectedEdge}
        onClose={() => { setShowMermaid(false); setSelected(null); setSelectedEdge(null); }}
        mermaidDraft={mermaidDraft} setMermaidDraft={setMermaidDraft}
        mermaidValid={mermaidValid} setMermaidValid={setMermaidValid} applyMermaid={applyMermaid}
        onEdgeConditionChange={(id, condition) => {
          patchEdgeCondition(id, condition);
          setSelectedEdge((s: any) => ({ ...s, data: { ...s.data, condition } }));
        }}
        onNodeChange={patchNode} onNodeDelete={deleteNode}
      />

      {/* Execution monitor */}
      {executionId && (
        <div className={`absolute left-1/2 z-20 -translate-x-1/2 ${drawerOpen ? '' : MOBILE_RAIL_CLEARANCE}`}
          style={drawerOpen ? { bottom: executionOffset } : undefined}>
          <ExecutionMonitor executionId={executionId} onNodeStatus={onNodeStatus} />
        </div>
      )}

      <OutputDrawer
        drawerOpen={drawerOpen} drawerExpanded={drawerExpanded} drawerHeight={drawerHeight} startResize={startResize}
        bottomTab={bottomTab} setBottomTab={setBottomTab} setDrawerExpanded={setDrawerExpanded}
        setDrawerOpen={setDrawerOpen} openDrawer={openDrawer}
        runsLoading={runsLoading} recentRuns={recentRuns} run={run} selectRun={selectRun}
        selectedRun={selectedRun} runDetail={runDetail} executionId={executionId}
        pipelineStage={pipelineStage}
        mermaidDraft={mermaidDraft} setMermaidDraft={setMermaidDraft}
        mermaidValid={mermaidValid} setMermaidValid={setMermaidValid} applyMermaid={applyMermaid}
      />

      {!nodes.length && !activeCat && !showAI && (
        <EmptyCanvasState
          onAddSource={() => setActiveCat('source')}
          onGenerateAI={() => setShowAI(true)}
        />
      )}
    </div>
  );
}
