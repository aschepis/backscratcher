package agent

type Agent struct {
	ID     string
	Config *AgentConfig
}

func NewAgent(id string, config *AgentConfig) *Agent {
	return &Agent{
		ID:     id,
		Config: config,
	}
}
