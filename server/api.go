package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
)

type ScrapeRequest struct {
	Keyword string `json:"keyword"`
}

type ScrapeResponse struct {
	Result string `json:"result"`
}

func scrapeHandler(w http.ResponseWriter, r *http.Request) {
	var req ScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid request"))
		return
	}

	// Call the collector CLI with the keyword
	cmd := exec.Command("go", "run", "../collector/main.go", req.Keyword)
	output, err := cmd.CombinedOutput()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error running collector"))
		return
	}

	resp := ScrapeResponse{Result: string(output)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func RegisterHandlers() {
	http.HandleFunc("/api/scrape", scrapeHandler)
}
