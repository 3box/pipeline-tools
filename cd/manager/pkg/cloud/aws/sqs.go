package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/3box/pipeline-tools/cd/manager/pkg/cloud"
)

var _ cloud.Queue = Sqs{}

const WaitTime = 1 * time.Second // 20 * time.Second
const MaxReceiveMsgs = 10
const VisibilityTimeout = 1 * time.Second // 3600 * time.Second

type Sqs struct {
	client *sqs.Client
	url    string
}

func NewSqs(cfg aws.Config, accountId, env string) cloud.Queue {
	region := os.Getenv("AWS_REGION")
	url := "https://sqs." + region + ".amazonaws.com/" + accountId + "/ceramic-" + env + "-ops.fifo"
	return &Sqs{sqs.NewFromConfig(cfg), url}
}

func (s Sqs) Send(ctx context.Context, job cloud.Job) (string, error) {
	//ctx, cancel := context.WithTimeout(ctx)
	//defer cancel()

	//attrs := make(map[string]*sqs.MessageAttributeValue, 0)
	//for _, attr := range req.Attributes {
	//	attrs[attr.Key] = &sqs.MessageAttributeValue{
	//		StringValue: aws.String(attr.Value),
	//		DataType:    aws.String(attr.Type),
	//	}
	//}

	//res, err := s.client.SendMessageWithContext(ctx, &sqs.SendMessageInput{
	//	MessageAttributes: attrs,
	//	MessageBody:       aws.String(req.Body),
	//	QueueUrl:          aws.String(s.url),
	//})
	//if err != nil {
	//	return "", fmt.Errorf("sqs: send error: %w", err)
	//}

	return "", nil
}

func (s Sqs) Receive(ctx context.Context) ([]cloud.Job, error) {
	// Must always be above `WaitTimeSeconds` otherwise `ReceiveMessage` will trigger a context timeout error.
	ctx, cancel := context.WithTimeout(ctx, WaitTime+1*time.Second)
	defer cancel()

	res, err := s.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:              aws.String(s.url),
		MaxNumberOfMessages:   MaxReceiveMsgs,
		WaitTimeSeconds:       int32(WaitTime.Seconds()),
		AttributeNames:        []types.QueueAttributeName{types.QueueAttributeNameAll},
		MessageAttributeNames: []string{string(types.QueueAttributeNameAll)},
		VisibilityTimeout:     int32(VisibilityTimeout.Seconds()),
	})
	if err != nil {
		return nil, fmt.Errorf("sqs: receive error: %w", err)
	}

	jobs := make([]cloud.Job, len(res.Messages))
	for idx, message := range res.Messages {
		jobs[idx].Id = *message.MessageId
		jobs[idx].RxId = *message.ReceiptHandle
		msgGrpId := message.Attributes["MessageGroupId"]
		switch msgGrpId {
		case "deploy":
			{
				jobs[idx].Type = cloud.JobType_Deploy
				// Parse deployment parameters
				if err = json.Unmarshal([]byte(*message.Body), &jobs[idx].Params); err != nil {
					return nil, fmt.Errorf("sqs: could not unmarshal job: %w", err)
				}
			}
		case "anchor":
			jobs[idx].Type = cloud.JobType_Anchor
		case "e2e":
			jobs[idx].Type = cloud.JobType_E2E
		case "smoke":
			jobs[idx].Type = cloud.JobType_Smoke
		default:
			return nil, fmt.Errorf("sqs: invalid job: %v", message)
		}
	}
	return jobs, nil
}

func (s Sqs) Delete(ctx context.Context, rxId string) error {
	ctx, cancel := context.WithTimeout(ctx, WaitTime)
	defer cancel()

	if _, err := s.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(s.url),
		ReceiptHandle: aws.String(rxId),
	}); err != nil {
		return fmt.Errorf("sqs: delete error: %w", err)
	}

	return nil
}
