// Command lambda is the AWS Lambda entrypoint, invoked via a Lambda
// Function URL that receives GitHub App webhook deliveries directly (no
// API Gateway).
package main

import (
	"context"
	"errors"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/emmahsax/github-pr-slack-notifier/internal/config"
	"github.com/emmahsax/github-pr-slack-notifier/internal/handler"
)

var h *handler.Handler

func init() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	h, err = handler.New(cfg)
	if err != nil {
		log.Fatalf("handler: %v", err)
	}
}

func handleRequest(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	eventType := req.Headers["x-github-event"]
	signature := req.Headers["x-hub-signature-256"]
	body := []byte(req.Body)

	if eventType == "" || signature == "" {
		return events.LambdaFunctionURLResponse{StatusCode: 400, Body: "missing required headers"}, nil
	}

	err := h.Handle(ctx, eventType, signature, body)
	switch {
	case err == nil:
		return events.LambdaFunctionURLResponse{StatusCode: 200, Body: "ok"}, nil
	case errors.Is(err, handler.ErrInvalidSignature):
		// Never retryable — GitHub only retries 5xx, and a bad signature
		// will never verify no matter how many times it's redelivered.
		log.Printf("rejected %s event: %v", eventType, err)
		return events.LambdaFunctionURLResponse{StatusCode: 401, Body: "rejected"}, nil
	default:
		// A transient GitHub/Slack API failure: return 5xx so GitHub
		// retries the delivery instead of silently dropping it.
		log.Printf("error handling %s event: %v", eventType, err)
		return events.LambdaFunctionURLResponse{StatusCode: 500, Body: "internal error"}, nil
	}
}

func main() {
	lambda.Start(handleRequest)
}
