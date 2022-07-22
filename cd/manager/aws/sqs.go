package aws

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/3box/pipeline-tools/cd/manager"
)

var _ manager.Queue = Sqs{}

const WaitTime = 20 * time.Second
const MaxReceiveMsgs = 10
const VisibilityTimeout = 30 * time.Second

type Sqs struct {
	client *sqs.Client
	url    string
}

func NewSqs(cfg aws.Config) manager.Queue {
	queueName := os.Getenv("QUEUE_NAME")
	q := &Sqs{sqs.NewFromConfig(cfg), queueName}
	// This block should only be needed when testing locally. None of the other services need mocked resources because
	// all processing is driven by the SQS queue.
	_, err := q.client.GetQueueUrl(context.Background(), &sqs.GetQueueUrlInput{QueueName: aws.String(queueName)})
	if err != nil {
		in := sqs.CreateQueueInput{
			QueueName: aws.String(queueName),
			Attributes: map[string]string{
				"DelaySeconds":                  "0",
				"ReceiveMessageWaitTimeSeconds": "20",
				"MessageRetentionPeriod":        "86400", // 1 day
				"FifoQueue":                     "true",
			},
		}
		_, err = q.client.CreateQueue(context.Background(), &in)
		log.Printf("sqs: queue creation failed: %v", err)
	}
	return q
}

func (s Sqs) Send(ctx context.Context, job manager.Job) (string, error) {
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

func (s Sqs) Receive(messages chan manager.QueueMessage, ctx context.Context) error {
	// Must always be above `WaitTimeSeconds` otherwise `ReceiveMessage` will trigger a context timeout error.
	ctx, cancel := context.WithTimeout(ctx, WaitTime+5*time.Second)
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
		return fmt.Errorf("sqs: receive error: %w", err)
	}

	log.Println(len(res.Messages))
	if len(res.Messages) < 1 {
		log.Println("No messages received")
	}

	for _, message := range res.Messages {
		log.Println("in message go")
		queueMessage := manager.QueueMessage{
			Id:         *message.MessageId,
			ReceiptId:  *message.ReceiptHandle,
			Attributes: message.Attributes,
			Body:       *message.Body,
		}
		messages <- queueMessage
	}
	return nil
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
