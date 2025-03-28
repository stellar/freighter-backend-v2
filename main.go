package main

import (
	"log"

	"github.com/stellar/freighter-backend-v2/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatalf("Error executing command: %s", err.Error())
	}
}
