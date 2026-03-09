package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/ishanchopra/gpu-scheduler/internal/sim"
)

func main() {
	var addr string
	flag.StringVar(&addr, "addr", ":8080", "Listen address for the simulator HTTP API.")
	flag.Parse()

	s := sim.NewSimulator()
	handler := sim.NewHTTPHandler(s)
	log.Fatal(http.ListenAndServe(addr, handler))
}
