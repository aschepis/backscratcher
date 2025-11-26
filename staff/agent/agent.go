package agent

import "github.com/aschepis/backscratcher/staff/config"

type Agent struct {
	ID     string
	Config *config.AgentConfig
}

func NewAgent(id string, config *config.AgentConfig) *Agent {
	return &Agent{
		ID:     id,
		Config: config,
	}
}
