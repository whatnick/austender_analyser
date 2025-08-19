package main

import (
	"context"
	"encoding/json"
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
	// Prefer query string params; fall back to JSON body
	keyword := req.QueryStringParameters["keyword"]
	company := req.QueryStringParameters["company"]
	agency := req.QueryStringParameters["agency"]

	if keyword == "" && req.Body != "" {
		var body struct {
			Keyword string `json:"keyword"`
			Company string `json:"company"`
			Agency  string `json:"agency"`
		}
		// Ignore JSON errors and keep defaults if body isn't valid JSON
		_ = json.Unmarshal([]byte(req.Body), &body)
		if keyword == "" {
			keyword = body.Keyword
		}
		if company == "" {
			company = body.Company
		}
		if agency == "" {
			agency = body.Agency
		}
	}

	// Sensible defaults: allow empty company/agency to mean no filter
	total, err := runScrape(keyword, company, agency)
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: 500, Body: "{\"error\":\"collector failed\"}", Headers: map[string]string{"Content-Type": "application/json"}}, nil
	}
	resp := ScrapeResponse{Result: total}
	b, _ := json.Marshal(resp)
	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       string(b),
		Headers:    map[string]string{"Content-Type": "application/json"},
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
