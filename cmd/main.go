package main

import (
	"log"
	"os"

	"github.com/GoogleCloudPlatform/functions-framework-go/funcframework"
	"github.com/Limechain/spreadsheet-baseline-connector"
)

func main() {
	funcframework.RegisterHTTPFunction("/update", functions.UpdateIncoming)
	funcframework.RegisterHTTPFunction("/sendProposals", functions.SendProposals)
	funcframework.RegisterHTTPFunction("/auth", functions.Authenticate)

	// Use PORT environment variable, or default to 8080.
	port := "9090"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	if err := funcframework.Start(port); err != nil {
		log.Fatalf("funcframework.Start: %v\n", err)
	}
}
