# internal/ghoten

Core execution engine. 203 Go files. Builds DAG graphs from config, walks them to plan/apply infrastructure changes.

## ARCHITECTURE

```
Context (entry point)
  ├── Plan()  → PlanGraphBuilder → Graph → ContextGraphWalker → NodePlannableResourceInstance
  ├── Apply() → ApplyGraphBuilder → Graph → ContextGraphWalker → NodeApplyableResourceInstance
  └── Stop()  → cancels runContext, signals walker
```

## KEY TYPES

| Type | File | Role |
|------|------|------|
| `Context` | `context.go` | Main orchestrator — holds plugins, hooks, parallelism semaphore |
| `Graph` | `graph.go` | DAG wrapper over `dag.AcyclicGraph` |
| `ContextGraphWalker` | `graph_walk_context.go` | Executes graph nodes with EvalContext |
| `EvalContext` / `BuiltinEvalContext` | `eval_context.go` | Runtime scope: providers, state, expression eval |
| `NodeAbstractResourceInstance` | `node_resource_abstract_instance.go` | Resource lifecycle (3446 lines, 40+ methods) |
| `PlanGraphBuilder` | `graph_builder_plan.go` | Builds plan graph via ordered transforms |
| `ApplyGraphBuilder` | `graph_builder_apply.go` | Builds apply graph from plan changes |

## KEY INTERFACES

| Interface | File | Methods |
|-----------|------|---------|
| `GraphBuilder` | `graph_builder.go` | `Build(ctx) (*Graph, tfdiags.Diagnostics)` |
| `GraphWalker` | `graph_walk.go` | `EvalContext() EvalContext`, `Execute(...)` |
| `GraphTransformer` | `transform.go` | `Transform(ctx, *Graph) error` |
| `GraphNodeExecutable` | `execute.go` | `Execute(ctx, EvalContext, walkOperation) tfdiags.Diagnostics` |
| `Hook` | `hook.go` | PreApply/PostApply, PreRefresh/PostRefresh, PostSkipRefresh (Ghoten-specific) |

## WHERE TO LOOK

| Task | Files | Notes |
|------|-------|-------|
| Smart refresh | `transform_smart_refresh.go` | Ghoten-specific: config hash comparison |
| Add graph transform | `transform_*.go` | Implement `GraphTransformer`, add to builder's Steps() |
| Resource planning | `node_resource_plan_instance.go` | `NodePlannableResourceInstance.Execute()` |
| Resource applying | `node_resource_apply_instance.go` | `NodeApplyableResourceInstance.Execute()` |
| Resource destruction | `node_resource_destroy.go` | `NodeDestroyResourceInstance` |
| Provider resolution | `transform_provider.go` | 1040 lines, dependency ordering |
| Expression evaluation | `evaluate.go` | 1161 lines, all HCL expression types |
| Import flow | `node_resource_plan_instance.go` | `managedResourceExecute()` handles import |

## ANTI-PATTERNS

- `node_resource_abstract_instance.go` is NOT a god object — don't try to split it
- Graph transforms MUST be idempotent — they may run multiple times
- `EvalContext` is scoped per module instance — don't assume global state
- Use `acquireRun()`/`releaseRun()` for concurrent access — never bypass
