package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestHandleLambdaRequest_Basic(t *testing.T) {
	// stub runScrape for determinism
	old := runScrape
	runScrape = func(keyword, company, agency string) (string, error) { return "$42.00", nil }
	defer func() { runScrape = old }()

	req := events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"keyword": "EY"},
	}
	resp, err := HandleLambdaRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out ScrapeResponse
	if err := json.Unmarshal([]byte(resp.Body), &out); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if out.Result != "$42.00" {
		t.Fatalf("unexpected result: %s", out.Result)
	}
}
