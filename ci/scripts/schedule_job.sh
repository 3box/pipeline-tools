#!/bin/bash

# This script is simple and creates a job in the CD manager DynamoDB table. Other scripts can invoke this one with the
# requisite job parameters filled in for various types of jobs.
docker run --rm -i \
  -e "AWS_REGION=$AWS_REGION" \
  -e "AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID" \
  -e "AWS_SECRET_ACCESS_KEY=$AWS_SECRET_ACCESS_KEY" \
  -v ~/.aws:/root/.aws \
  -v "$PWD":/aws \
  amazon/aws-cli dynamodb put-item --table-name "ceramic-$1-ops" --item "$2"
