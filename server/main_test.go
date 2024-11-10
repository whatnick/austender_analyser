package main

import (
	"context"
	"testing"
)

func TestHandleRequest(t *testing.T) {
	ctx := context.Background()
	event := MyEvent{Company: "EY"}

	expectedResponse := "Total spending on company EY!"
	expectedError := error(nil)

	response, err := HandleRequest(ctx, event)

	if response != expectedResponse {
		t.Errorf("Expected response: %s, but got: %s", expectedResponse, response)
	}

	if err != expectedError {
		t.Errorf("Expected error: %v, but got: %v", expectedError, err)
	}
}
