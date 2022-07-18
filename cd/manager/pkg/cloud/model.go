package cloud

import (
	"context"
)

type QueueMessage struct {
	Id         string
	ReceiptId  string
	Body       string
	Attributes map[string]string
}

type Queue interface {
	Receive(chan QueueMessage, context.Context) error
	Send(context.Context, Job) (string, error)
}

type JobType uint8

const (
	JobType_Invalid JobType = iota
	JobType_Deploy
	JobType_Anchor
	JobType_TestE2E
	JobType_TestSmoke
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
	Launch(string, string, string) error
	RestartService(string, string) error
	UpdateService(string, string) error
}
