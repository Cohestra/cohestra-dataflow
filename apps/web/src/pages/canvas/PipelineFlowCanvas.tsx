import ReactFlow, {
  Background, BackgroundVariant, Controls, MiniMap,
  type Connection, type Edge, type Node, type NodeTypes, type ReactFlowInstance,
} from 'reactflow';

// Thin wrapper around the React Flow canvas itself (graph, background dots,
// zoom controls, minimap). All graph state and interaction handlers stay in
// the page — this component only owns the "how does React Flow get rendered
// and positioned around the output drawer" concern.
export function PipelineFlowCanvas({
  nodes, edges, nodeTypes, onInit,
  onNodesChange, onEdgesChange, onConnect, onConnectStart, onConnectEnd,
  onNodeClick, onNodeActivate, onEdgeClick, onPaneClick,
  dark, byType, drawerOpen, drawerOffset,
}: {
  nodes: Node[];
  edges: Edge[];
  nodeTypes: NodeTypes;
  onInit: (instance: ReactFlowInstance) => void;
  onNodesChange: (changes: any) => void;
  onEdgesChange: (changes: any) => void;
  onConnect: (connection: Connection) => void;
  onConnectStart: (event: any, params: any) => void;
  onConnectEnd: (event: MouseEvent | TouchEvent) => void;
  onNodeClick: (event: any, node: Node) => void;
  onNodeActivate: (node: Node) => void;
  onEdgeClick: (event: any, edge: Edge) => void;
  onPaneClick: () => void;
  dark: boolean;
  byType: Record<string, { color?: string } | undefined>;
  drawerOpen: boolean;
  drawerOffset: number | string;
}) {
  return (
    <ReactFlow
      nodes={nodes} edges={edges} nodeTypes={nodeTypes}
      onInit={onInit}
      onNodesChange={onNodesChange} onEdgesChange={onEdgesChange}
      onConnect={onConnect}
      onConnectStart={onConnectStart}
      onConnectEnd={onConnectEnd}
      onNodeClick={onNodeClick}
      // React Flow's own Enter/Space handler only toggles graph selection;
      // bridge keyboard activation of a focused node to the inspector.
      onKeyDown={event => {
        if (event.key !== 'Enter' && event.key !== ' ') return;
        const target = event.target as HTMLElement;
        if (!target.classList.contains('react-flow__node')) return;
        const node = nodes.find(n => n.id === target.dataset.id);
        if (!node) return;
        event.preventDefault();
        onNodeActivate(node);
      }}
      onEdgeClick={onEdgeClick}
      onPaneClick={onPaneClick}
      fitView
      className="absolute inset-0">
      <Background variant={BackgroundVariant.Dots} gap={24} size={1}
        color={dark ? 'rgba(255,255,255,0.07)' : 'rgba(0,0,0,0.07)'} />
      <Controls position="bottom-left" style={{ left: 72, bottom: drawerOpen ? drawerOffset : 12 }} />
      {nodes.length > 0 && <MiniMap position="bottom-left" pannable zoomable
        style={{ left: 126, bottom: drawerOpen ? drawerOffset : 12, width: 190, height: 112 }}
        nodeColor={n => byType[n.data.activityType]?.color ?? '#6965db'}
        nodeStrokeColor={dark ? 'rgba(255,255,255,0.45)' : 'rgba(0,0,0,0.25)'} nodeStrokeWidth={2}
        maskColor={dark ? 'rgba(8,10,18,0.78)' : 'rgba(235,237,245,0.78)'} />}
    </ReactFlow>
  );
}
