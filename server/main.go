package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	collector "github.com/whatnick/austender_analyser/collector/cmd"

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
	startDate := req.QueryStringParameters["startDate"]
	endDate := req.QueryStringParameters["endDate"]
	dateType := req.QueryStringParameters["dateType"]

	if keyword == "" && req.Body != "" {
		var body struct {
			Keyword     string `json:"keyword"`
			Company     string `json:"company"`
			CompanyName string `json:"companyName"`
			Agency      string `json:"agency"`
			StartDate   string `json:"startDate"`
			EndDate     string `json:"endDate"`
			DateType    string `json:"dateType"`
		}
		// Ignore JSON errors and keep defaults if body isn't valid JSON
		_ = json.Unmarshal([]byte(req.Body), &body)
		if keyword == "" {
			keyword = body.Keyword
		}
		if company == "" {
			if body.Company != "" {
				company = body.Company
			} else {
				company = body.CompanyName
			}
		}
		if agency == "" {
			agency = body.Agency
		}
		if startDate == "" {
			startDate = body.StartDate
		}
		if endDate == "" {
			endDate = body.EndDate
		}
		if dateType == "" {
			dateType = body.DateType
		}
	}
	start, err := parseRequestDate(startDate)
	if err != nil {
		return clientErrorResponse(fmt.Sprintf("invalid startDate: %v", err)), nil
	}
	end, err := parseRequestDate(endDate)
	if err != nil {
		return clientErrorResponse(fmt.Sprintf("invalid endDate: %v", err)), nil
	}

	// Sensible defaults: allow empty filters
	total, err := runScrape(ctx, collector.SearchRequest{
		Keyword:   keyword,
		Company:   company,
		Agency:    agency,
		StartDate: start,
		EndDate:   end,
		DateType:  dateType,
	})
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

func clientErrorResponse(msg string) events.APIGatewayProxyResponse {
	return events.APIGatewayProxyResponse{
		StatusCode: 400,
		Body:       fmt.Sprintf("{\"error\":%q}", strings.ReplaceAll(msg, "\"", "'")),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
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
