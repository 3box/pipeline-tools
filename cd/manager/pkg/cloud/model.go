package cloud

import "context"

type Queue interface {
	Receive(context.Context) ([]Job, error)
	Send(context.Context, Job) (string, error)
}

type JobType uint8

const (
	JobTypeInvalid JobType = iota
	JobType_Deploy
	JobType_Anchor
	JobType_E2E
	JobType_Smoke
)

type Job struct {
	Type   JobType
	Id     string
	RxId   string
	Params map[string]interface{}
}

type State struct {
	Ts int
	Id string
}

type Database interface {
	GetState(context.Context) (*State, error)
	UpdateState(context.Context, *State) error
}

type Deployment interface {
	LaunchService(string, string) error
	RestartService(string, string) error
	UpdateService(string, string) error
}
