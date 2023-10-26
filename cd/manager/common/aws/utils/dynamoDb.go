package utils

import (
	"context"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const defaultHttpWaitTime = 30 * time.Second
const tableCreationRetries = 3
const tableCreationWait = 3 * time.Second

func CreateTable(ctx context.Context, client *dynamodb.Client, createTableIn *dynamodb.CreateTableInput) error {
	if exists, err := TableExists(ctx, client, *createTableIn.TableName); !exists {
		httpCtx, httpCancel := context.WithTimeout(ctx, defaultHttpWaitTime)
		defer httpCancel()

		if _, err = client.CreateTable(httpCtx, createTableIn); err != nil {
			return err
		}
		for i := 0; i < tableCreationRetries; i++ {
			if exists, err = TableExists(ctx, client, *createTableIn.TableName); exists {
				return nil
			}
			time.Sleep(tableCreationWait)
		}
		return err
	}
	return nil
}

func TableExists(ctx context.Context, client *dynamodb.Client, table string) (bool, error) {
	httpCtx, httpCancel := context.WithTimeout(ctx, defaultHttpWaitTime)
	defer httpCancel()

	if output, err := client.DescribeTable(httpCtx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}); err != nil {
		return false, err
	} else {
		return output.Table.TableStatus == types.TableStatusActive, nil
	}
}

func TsDecode(ts string) (time.Time, error) {
	nsec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, nsec), nil
}
