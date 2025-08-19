package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

type MyEvent struct {
	Company string `json:"name"`
}

// Lambda handler for API Gateway
func HandleLambdaRequest(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	keyword := req.QueryStringParameters["keyword"]
	// Simulate calling collector (replace with actual logic)
	result := fmt.Sprintf("Total spending on company %s!", keyword)
	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       result,
	}, nil
}

func main() {
	mode := os.Getenv("AUSTENDER_MODE")
	if mode == "lambda" {
		lambda.Start(HandleLambdaRequest)
	} else {
		RegisterHandlers()
		fmt.Println("Server running on :8080")
		http.ListenAndServe(":8080", nil)
	}
}
