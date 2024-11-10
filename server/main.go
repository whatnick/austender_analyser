package main

import (
	"context"
	"fmt"

	//begin import
	"github.com/aws/aws-lambda-go/lambda"
	//end import
)

// begin function
type MyEvent struct {
	Company string `json:"name"`
}

func HandleRequest(ctx context.Context, name MyEvent) (string, error) {
	return fmt.Sprintf("Total spending on company %s!", name.Company), nil
}

func main() {
	lambda.Start(HandleRequest)
}

//end function
