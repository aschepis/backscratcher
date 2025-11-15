# Scheduling Support in Agent Configs\*\*

### Goals

Extend config-based agents to specify schedules and support next-wake times.

### Deliverables

- Add to `AgentConfig`:

  ```yaml
  schedule: "every 15 minutes" # OR cron-like string
  enabled: true
  ```

- Create parser that converts `schedule` → a `next_wake` timestamp
- Store next wake in `agent_states`

### Required Changes

- Modify config loader to extract schedule expressions
- Modify initialization code to compute first `next_wake` for enabled agents

### Tests

- Agent with schedule loads with correct next_wake
- Changing schedule reflects on re-load

## State-Driven Scheduler\*\*

_(Not cron itself—cron → next_wake translation only.)_

### Goals

Implement a background scheduler that wakes agents based on `waiting_external` + `next_wake`.

### Deliverables

- A small Go goroutine under `staff/runtime/scheduler.go`
- Logic:

  - Every N seconds:

    ```
    SELECT * FROM agent_states
    WHERE status='waiting_external' AND next_wake <= NOW()
    ```

  - Call AgentRunner with `"continue"` input

- Automatically compute next `next_wake` after each run using schedule rules

### Tests

- Agent wakes automatically
- A scheduled agent runs repeatedly on interval
- Agent moves to `waiting_external` after the run and sets next_wake
