package service

import "github.com/monoes/mono-agent/internal/workflow"

func RegisterAll(r *workflow.NodeTypeRegistry) {
	RegisterGroupA(r)
	RegisterGroupB(r)
}
