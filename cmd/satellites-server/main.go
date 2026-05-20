package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/bobmcallan/satellites/internal/mcpserver"
)

func main() {
	addr := os.Getenv("SATELLITES_LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	s := mcpserver.New()
	handler := mcpserver.HTTPHandler(s)

	log.Printf("satellites-server listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
