package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
	"github.com/3box/pipeline-tools/cd/manager/common/job"
)

func Setup(addr string, m manager.Manager) http.Server {
	logger := log.New(os.Stdout, "http: ", log.LstdFlags)
	mux := http.NewServeMux()
	mux.Handle("/healthcheck", healthcheckHandler())
	mux.Handle("/time", timeHandler(time.RFC1123))
	mux.Handle("/job", jobHandler(m))
	mux.Handle("/pause", pauseHandler(m))
	return http.Server{
		Addr:     addr,
		Handler:  logging(logger)(mux),
		ErrorLog: logger,
	}
}

func healthcheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Alive!\n"))
	}
}

func pauseHandler(m manager.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.Pause()
	}
}

func timeHandler(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tm := time.Now().Format(format)
		status := http.StatusOK
		message := "The time is " + tm
		writeJsonResponse(w, message, status)
	}
}

func jobHandler(m manager.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		var body any
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		jobState := job.JobState{}
		if r.Header.Get("Content-Type") != "application/json" {
			status = http.StatusUnsupportedMediaType
			body = "content-type is not application/json"
		} else if err := decoder.Decode(&jobState); err != nil {
			status = http.StatusBadRequest
			var unmarshalErr *json.UnmarshalTypeError
			if errors.As(err, &unmarshalErr) {
				body = "wrong type for field: " + unmarshalErr.Field
			} else {
				body = "bad request: " + err.Error()
			}
		} else if r.Method == http.MethodPost {
			if jobState, err = m.NewJob(jobState); err != nil {
				status = http.StatusInternalServerError
				body = "could not queue job: " + err.Error()
			} else {
				body = jobState
			}
		} else if r.Method == http.MethodGet {
			body = m.CheckJob(jobState.JobId)
		} else {
			body = "unsupported method: " + r.Method
			status = http.StatusMethodNotAllowed
		}
		writeJsonResponse(w, body, status)
	}
}

func writeJsonResponse(w http.ResponseWriter, body any, httpStatusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatusCode)
	jsonResp, _ := json.Marshal(body)
	w.Write(jsonResp)
}

func logging(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				logger.Println(r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
			}()
			next.ServeHTTP(w, r)
		})
	}
}
