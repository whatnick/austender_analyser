package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	collector "github.com/whatnick/austender_analyser/collector/cmd"
)

func TestHandleLambdaRequest(t *testing.T) {
	// stub runScrape
	old := runScrape
	runScrape = func(ctx context.Context, req collector.SearchRequest) (string, error) {
		if req.Keyword != "EY" {
			t.Fatalf("unexpected keyword: %s", req.Keyword)
		}
		return "$77.00", nil
	}
	defer func() { runScrape = old }()

	req := events.APIGatewayProxyRequest{QueryStringParameters: map[string]string{"keyword": "EY"}}
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
	if out.Result != "$77.00" {
		t.Fatalf("unexpected result: %s", out.Result)
	}
}
