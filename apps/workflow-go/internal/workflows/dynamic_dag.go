package workflows

import (
	"fmt"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type workflowState struct {
	Paused    bool
	Cancelled bool
	Results   map[string]model.NodeResult
}

type executionPlan struct {
	Levels   [][]model.Node
	Incoming map[string][]model.Edge
}

func DynamicDAGWorkflow(ctx workflow.Context, input model.WorkflowInput) (model.ExecutionStatus, error) {
	info := workflow.GetInfo(ctx)
	if input.ExecutionID == "" {
		input.ExecutionID = "exec-" + info.WorkflowExecution.RunID
	}
	if input.Environment == "" {
		input.Environment = model.Environment(info.Namespace)
	}
	if input.Trigger.FiredAt == "" {
		input.Trigger.FiredAt = workflow.Now(ctx).UTC().Format(time.RFC3339Nano)
	}

	// Existing histories retain the original global options, including any
	// previously ignored node settings. Validate new runs before any activity.
	nodePolicies := workflow.GetVersion(ctx, "node-activity-policy-v1", workflow.DefaultVersion, 1) != workflow.DefaultVersion
	if nodePolicies {
		if err := model.ValidateNodePolicies(input.Definition); err != nil {
			return model.ExecutionStatus{}, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidNodePolicy", err)
		}
	}

	activityOptions := workflow.ActivityOptions{
		TaskQueue:           "dynamic-activities-" + string(input.Environment),
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        2 * time.Second,
			BackoffCoefficient:     2,
			MaximumInterval:        5 * time.Minute,
			MaximumAttempts:        5,
			NonRetryableErrorTypes: []string{"UnknownActivityError", "SchemaValidationError", "QuotaExceededError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	if input.Trigger.Type == "cron" && !input.ExecutionPrepared {
		if input.PipelineRowID == "" {
			return model.ExecutionStatus{}, fmt.Errorf("scheduled workflow is missing pipelineRowId")
		}
		err := workflow.ExecuteActivity(ctx, "prepareScheduledExecution", map[string]interface{}{
			"executionId":   input.ExecutionID,
			"pipelineRowId": input.PipelineRowID,
			"tenantId":      input.TenantID,
			"environment":   input.Environment,
			"workflowId":    info.WorkflowExecution.ID,
			"runId":         info.WorkflowExecution.RunID,
		}).Get(ctx, nil)
		if err != nil {
			return model.ExecutionStatus{}, err
		}
		input.ExecutionPrepared = true
	}

	plan, err := buildPlan(input.Definition.Nodes, input.Definition.Edges)
	if err != nil {
		return model.ExecutionStatus{}, err
	}
	state := &workflowState{Results: map[string]model.NodeResult{}}
	startedAt := workflow.Now(ctx).UTC().Format(time.RFC3339Nano)

	if err := workflow.SetQueryHandler(ctx, "status", func() (model.ExecutionStatus, error) {
		phase := "running"
		if state.Cancelled {
			phase = "cancelled"
		} else if state.Paused {
			phase = "paused"
		}
		return model.ExecutionStatus{
			ExecutionID: input.ExecutionID,
			Phase:       phase,
			NodeResults: state.Results,
			StartedAt:   startedAt,
		}, nil
	}); err != nil {
		return model.ExecutionStatus{}, err
	}

	pauseCh := workflow.GetSignalChannel(ctx, "pause")
	resumeCh := workflow.GetSignalChannel(ctx, "resume")
	cancelCh := workflow.GetSignalChannel(ctx, "cancel")

	maxParallel := 5
	if input.Definition.Concurrency != nil && input.Definition.Concurrency.MaxParallelNodes > 0 {
		maxParallel = input.Definition.Concurrency.MaxParallelNodes
	}

	for _, level := range plan.Levels {
		drainSignals(pauseCh, resumeCh, cancelCh, state)
		for state.Paused && !state.Cancelled {
			selector := workflow.NewSelector(ctx)
			selector.AddReceive(resumeCh, func(c workflow.ReceiveChannel, _ bool) {
				c.Receive(ctx, nil)
				state.Paused = false
			})
			selector.AddReceive(cancelCh, func(c workflow.ReceiveChannel, _ bool) {
				c.Receive(ctx, nil)
				state.Cancelled = true
			})
			selector.Select(ctx)
		}
		if state.Cancelled {
			break
		}

		for start := 0; start < len(level); start += maxParallel {
			end := start + maxParallel
			if end > len(level) {
				end = len(level)
			}
			futures := make([]workflow.Future, 0, end-start)
			for _, node := range level[start:end] {
				node := node
				future, settable := workflow.NewFuture(ctx)
				workflow.Go(ctx, func(ctx workflow.Context) {
					if nodePolicies {
						ctx = withNodeActivityOptions(ctx, node)
					}
					result, runErr := runNode(ctx, input, node, plan.Incoming, state.Results)
					if runErr != nil {
						settable.SetError(runErr)
						return
					}
					settable.Set(result, nil)
				})
				futures = append(futures, future)
			}
			for index, future := range futures {
				var result model.NodeResult
				if err := future.Get(ctx, &result); err != nil {
					return model.ExecutionStatus{}, err
				}
				state.Results[level[start+index].ID] = result
			}
		}
	}

	phase := "completed"
	if state.Cancelled {
		phase = "cancelled"
	} else {
		for _, result := range state.Results {
			if result.Status == "failed" {
				phase = "failed"
				break
			}
		}
	}
	if phase == "completed" {
		cursors := make([]map[string]interface{}, 0)
		dedupeCheckpoints := make([]map[string]interface{}, 0)
		for _, result := range state.Results {
			checkpoint, ok := result.Meta["checkpoint"].(map[string]interface{})
			connectionID, hasID := result.Meta["connectionId"].(string)
			if ok && hasID {
				cursors = append(cursors, map[string]interface{}{"connectionId": connectionID, "checkpoint": checkpoint})
			}
			if checkpoint, ok := result.Meta["dedupeCheckpoint"].(map[string]interface{}); ok {
				dedupeCheckpoints = append(dedupeCheckpoints, checkpoint)
			}
		}
		if len(cursors) > 0 {
			if err := workflow.ExecuteActivity(ctx, "commitSourceCursors", map[string]interface{}{
				"tenantId": input.TenantID, "cursors": cursors,
			}).Get(ctx, nil); err != nil {
				return model.ExecutionStatus{}, err
			}
		}
		if len(dedupeCheckpoints) > 0 {
			if err := workflow.ExecuteActivity(ctx, "commitDedupeKeys", map[string]interface{}{
				"tenantId": input.TenantID, "checkpoints": dedupeCheckpoints,
			}).Get(ctx, nil); err != nil {
				return model.ExecutionStatus{}, err
			}
		}
	}
	if err := workflow.ExecuteActivity(ctx, "markExecution", map[string]interface{}{
		"executionId": input.ExecutionID,
		"phase":       phase,
	}).Get(ctx, nil); err != nil {
		return model.ExecutionStatus{}, err
	}
	return model.ExecutionStatus{
		ExecutionID: input.ExecutionID,
		Phase:       phase,
		NodeResults: state.Results,
		StartedAt:   startedAt,
		CompletedAt: workflow.Now(ctx).UTC().Format(time.RFC3339Nano),
	}, nil
}

// withNodeActivityOptions scopes overrides to this node's activities (including
// every source page). Execution bookkeeping keeps the workflow defaults.
func withNodeActivityOptions(ctx workflow.Context, node model.Node) workflow.Context {
	options := workflow.GetActivityOptions(ctx)
	if node.TimeoutSec != nil {
		options.StartToCloseTimeout = time.Duration(*node.TimeoutSec) * time.Second
		if options.HeartbeatTimeout > options.StartToCloseTimeout {
			options.HeartbeatTimeout = options.StartToCloseTimeout
		}
	}
	if node.Retry != nil && node.Retry.MaximumAttempts != nil {
		// Activity options share a policy pointer; never mutate parallel siblings.
		policy := *options.RetryPolicy
		policy.MaximumAttempts = int32(*node.Retry.MaximumAttempts)
		options.RetryPolicy = &policy
	}
	return workflow.WithActivityOptions(ctx, options)
}

func runNode(
	ctx workflow.Context,
	input model.WorkflowInput,
	node model.Node,
	incoming map[string][]model.Edge,
	results map[string]model.NodeResult,
) (model.NodeResult, error) {
	inEdges := incoming[node.ID]
	successful := false
	for _, edge := range inEdges {
		if upstream, ok := results[edge.Source]; ok && upstream.Status == "success" {
			successful = true
		}
	}
	if len(inEdges) > 0 && !successful {
		return skipped(node.ID), nil
	}

	for _, edge := range inEdges {
		if edge.Condition == "" {
			continue
		}
		upstream := results[edge.Source]
		var allowed bool
		err := workflow.ExecuteActivity(ctx, "evalEdgeCondition", map[string]interface{}{
			"condition":    edge.Condition,
			"inputRef":     upstream.OutputRef,
			"encryptedDek": input.EncryptedDEK,
		}).Get(ctx, &allowed)
		if err != nil {
			return failed(node.ID, err), nil
		}
		if !allowed {
			return skipped(node.ID), nil
		}
	}

	if node.Type == "source" {
		return runSource(ctx, input, node)
	}

	refs := make([]*model.DataRef, 0, len(inEdges))
	for _, edge := range inEdges {
		if ref := results[edge.Source].OutputRef; ref != nil {
			refs = append(refs, ref)
		}
	}
	if node.Type == "merge" {
		var result model.NodeResult
		err := workflow.ExecuteActivity(ctx, "mergeRefs", map[string]interface{}{
			"inputRefs":    refs,
			"strategy":     defaultString(node.MergeStrategy, "concat"),
			"joinKey":      node.JoinKey,
			"tenantId":     input.TenantID,
			"executionId":  input.ExecutionID,
			"nodeId":       node.ID,
			"encryptedDek": input.EncryptedDEK,
		}).Get(ctx, &result)
		if err != nil {
			return failed(node.ID, err), nil
		}
		return result, nil
	}

	var inputRef *model.DataRef
	if len(refs) > 0 {
		inputRef = refs[0]
	} else {
		inputRef = input.Trigger.PayloadRef
	}
	if node.Type == "fork" {
		return model.NodeResult{
			NodeID: node.ID, Status: "success", OutputRef: inputRef,
			Meta: map[string]interface{}{"durationMs": 0},
		}, nil
	}

	var result model.NodeResult
	err := workflow.ExecuteActivity(ctx, "dispatchNode", map[string]interface{}{
		"activityType":    node.ActivityType,
		"config":          node.Config,
		"inputRef":        inputRef,
		"tenantId":        input.TenantID,
		"executionId":     input.ExecutionID,
		"nodeId":          node.ID,
		"pipelineVersion": input.Definition.Version,
		"encryptedDek":    input.EncryptedDEK,
	}).Get(ctx, &result)
	if err != nil {
		return failed(node.ID, err), nil
	}
	return result, nil
}

func runSource(ctx workflow.Context, input model.WorkflowInput, node model.Node) (model.NodeResult, error) {
	total := 0
	refs := make([]*model.DataRef, 0)
	var checkpoint map[string]interface{}
	connectionID := sourceConnectionID(input.Definition.ID, node)
	for pageNumber := 1; ; pageNumber++ {
		var page struct {
			OutputRef   *model.DataRef         `json:"outputRef"`
			HasMore     bool                   `json:"hasMore"`
			RecordCount int                    `json:"recordCount"`
			Checkpoint  map[string]interface{} `json:"checkpoint,omitempty"`
		}
		err := workflow.ExecuteActivity(ctx, "fetchSourcePage", map[string]interface{}{
			"activityType": node.ActivityType,
			"config":       node.Config,
			"ingestion":    node.Ingestion,
			"tenantId":     input.TenantID,
			"connectionId": connectionID,
			"cursor":       checkpoint,
			"executionId":  input.ExecutionID,
			"nodeId":       node.ID,
			"encryptedDek": input.EncryptedDEK,
		}).Get(ctx, &page)
		if err != nil {
			return failed(node.ID, err), nil
		}
		if page.OutputRef != nil {
			refs = append(refs, page.OutputRef)
		}
		if page.Checkpoint != nil {
			checkpoint = page.Checkpoint
		}
		total += page.RecordCount
		// ponytail: bound history/payload size; a later execution resumes from the saved cursor.
		if !page.HasMore {
			break
		}
		if pageNumber >= 50 {
			return failed(node.ID, fmt.Errorf("source partition exceeded 50 pages; reduce backfill partition size or increase pageSize")), nil
		}
	}
	var outputRef *model.DataRef
	if len(refs) == 1 {
		outputRef = refs[0]
	} else if len(refs) > 1 {
		var merged model.NodeResult
		if err := workflow.ExecuteActivity(ctx, "mergeRefs", map[string]interface{}{
			"inputRefs": refs, "strategy": "concat", "tenantId": input.TenantID,
			"executionId": input.ExecutionID, "nodeId": node.ID,
			"encryptedDek": input.EncryptedDEK,
		}).Get(ctx, &merged); err != nil {
			return failed(node.ID, err), nil
		}
		outputRef = merged.OutputRef
	}
	meta := map[string]interface{}{"durationMs": 0, "recordCount": total}
	if checkpoint != nil {
		meta["checkpoint"] = checkpoint
		meta["connectionId"] = connectionID
	}
	return model.NodeResult{
		NodeID: node.ID, Status: "success", OutputRef: outputRef, Meta: meta,
	}, nil
}

func sourceConnectionID(pipelineID string, node model.Node) string {
	id := pipelineID + ":" + node.ID
	if node.Config["syncMode"] == "cdc" {
		id += ":cdc"
	}
	if node.Ingestion != nil && node.Ingestion.StateKey != "" {
		id += ":" + node.Ingestion.StateKey
	}
	return id
}

func buildPlan(nodes []model.Node, edges []model.Edge) (executionPlan, error) {
	inDegree := map[string]int{}
	incoming := map[string][]model.Edge{}
	outgoing := map[string][]model.Edge{}
	byID := map[string]model.Node{}
	for _, node := range nodes {
		inDegree[node.ID] = 0
		byID[node.ID] = node
	}
	for _, edge := range edges {
		if _, ok := byID[edge.Source]; !ok {
			return executionPlan{}, fmt.Errorf("edge references unknown source %q", edge.Source)
		}
		if _, ok := byID[edge.Target]; !ok {
			return executionPlan{}, fmt.Errorf("edge references unknown target %q", edge.Target)
		}
		inDegree[edge.Target]++
		incoming[edge.Target] = append(incoming[edge.Target], edge)
		outgoing[edge.Source] = append(outgoing[edge.Source], edge)
	}
	queue := make([]model.Node, 0)
	for _, node := range nodes {
		if inDegree[node.ID] == 0 {
			queue = append(queue, node)
		}
	}
	levels := make([][]model.Node, 0)
	visited := 0
	for len(queue) > 0 {
		level := append([]model.Node{}, queue...)
		levels = append(levels, level)
		visited += len(level)
		next := make([]model.Node, 0)
		for _, node := range level {
			for _, edge := range outgoing[node.ID] {
				inDegree[edge.Target]--
				if inDegree[edge.Target] == 0 {
					next = append(next, byID[edge.Target])
				}
			}
		}
		queue = next
	}
	if visited != len(nodes) {
		return executionPlan{}, fmt.Errorf("pipeline contains a cycle")
	}
	return executionPlan{Levels: levels, Incoming: incoming}, nil
}

func drainSignals(
	pause workflow.ReceiveChannel,
	resume workflow.ReceiveChannel,
	cancel workflow.ReceiveChannel,
	state *workflowState,
) {
	for pause.ReceiveAsync(nil) {
		state.Paused = true
	}
	for resume.ReceiveAsync(nil) {
		state.Paused = false
	}
	for cancel.ReceiveAsync(nil) {
		state.Cancelled = true
	}
}

func skipped(nodeID string) model.NodeResult {
	return model.NodeResult{
		NodeID: nodeID, Status: "skipped",
		Meta: map[string]interface{}{"durationMs": 0},
	}
}

func failed(nodeID string, err error) model.NodeResult {
	return model.NodeResult{
		NodeID: nodeID, Status: "failed", Error: err.Error(),
		Meta: map[string]interface{}{"durationMs": 0},
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
