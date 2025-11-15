# Agent State Machine Support\*\*

_(Smallest foundational change; unblocks scheduler + MCP + everything else.)_

### Goals

Add persistent, queryable agent state so agents can be paused, resumed, scheduled, and woken.

### Deliverables

- New table `agent_states`
- Getter + Upsert APIs in Go
- Integration with AgentRunner entry/exit
- Agent states:

  - `idle`
  - `running`
  - `waiting_human`
  - `waiting_external`
  - `sleeping`

### Required Changes

- Add `agent_state.go` under `staff/agent/`
- Extend AgentRunner to:

  - mark agent as `running` when execution begins
  - leave state as set by tool (e.g. `waiting_human` via notification)
  - mark agent as `idle` when run completes normally

### Tests

- Agent started → `running`
- Agent notifies user → state becomes `waiting_human`
- Manual resume → `running` → `idle`
