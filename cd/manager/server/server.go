package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/3box/pipeline-tools/cd/manager"
)

func Setup(addr string, m manager.Manager) http.Server {
	logger := log.New(os.Stdout, "http: ", log.LstdFlags)
	mux := http.NewServeMux()
	mux.Handle("/healthcheck", healthcheckHandler())
	mux.Handle("/time", timeHandler(time.RFC1123))
	mux.Handle("/job", jobHandler(m))
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
		headerContentType := r.Header.Get("Content-Type")
		if headerContentType != "application/json" {
			writeJsonResponse(w, "content-type is not application/json", http.StatusUnsupportedMediaType)
			return
		}
		status := http.StatusOK
		message := "Success"
		decoder := json.NewDecoder(r.Body)
		jobState := manager.JobState{}
		// Allow unknown fields so that we ignore unneeded params sent by calling services.
		//decoder.DisallowUnknownFields()
		if err := decoder.Decode(&jobState); err != nil {
			status = http.StatusBadRequest
			var unmarshalErr *json.UnmarshalTypeError
			if errors.As(err, &unmarshalErr) {
				message = "wrong type for field " + unmarshalErr.Field
			} else {
				message = "bad request: " + err.Error()
			}
		} else if r.Method == http.MethodPost {
			if jobId, err := m.NewJob(jobState); err != nil {
				status = http.StatusBadRequest
				message = "could not queue job: " + err.Error()
			} else {
				message = jobId
			}
		} else if r.Method == http.MethodGet {
			message = m.CheckJob(jobState.Id)
		}
		writeJsonResponse(w, message, status)
	}
}

func writeJsonResponse(w http.ResponseWriter, message string, httpStatusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatusCode)
	resp := make(map[string]string)
	resp["message"] = message
	jsonResp, _ := json.Marshal(resp)
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
