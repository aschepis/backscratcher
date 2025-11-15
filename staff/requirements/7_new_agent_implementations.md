# Agent Implementations (Email, Calendar, Introspection)

### Goals

Create three agents powered by schedules + MCP + memory.

Each agent uses:

- `memory_normalize`
- `memory_search_personal`
- `notification_send`
- MCP tools (gmail/calendar)

### Deliverables

- Config updates in `agents.yaml`
- Example:

  ```yaml
  email_triage:
    schedule: "every 15 minutes"
    tools:
      - gmail_messages_list
      - gmail_messages_get
      - gmail_messages_modify
      - memory_search_personal
      - memory_normalize
      - notification_send
  ```

### Tests

- Email agent wakes on schedule, triages inbox
- Calendar agent wakes on schedule, handles invitations
- Introspection agent analyzes logs & suggests improvements
