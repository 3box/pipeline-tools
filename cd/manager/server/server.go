package server

import (
	"context"
	"net/http"
	"time"
)

func healthcheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Alive!\n"))
	}
}

func timeHandler(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tm := time.Now().Format(format)
		w.Write([]byte("The time is: " + tm))
	}
}

func SetUp(addr string, ctx context.Context) http.Server {
	mux := http.NewServeMux()
	mux.Handle("/healthcheck", healthcheckHandler())
	mux.Handle("/time", timeHandler(time.RFC1123))
	return http.Server{Addr: addr, Handler: mux}
}
