package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/3box/pipeline-tools/cd/manager"
)

func Setup(addr string, jq manager.Manager) http.Server {
	mux := http.NewServeMux()
	mux.Handle("/healthcheck", healthcheckHandler())
	mux.Handle("/time", timeHandler(time.RFC1123))
	mux.Handle("/job", jobHandler(jq))
	return http.Server{Addr: addr, Handler: mux}
}

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

func jobHandler(jq manager.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headerContentTtype := r.Header.Get("Content-Type")
		if headerContentTtype != "application/json" {
			errorResponse(w, "Content Type is not application/json", http.StatusUnsupportedMediaType)
			return
		}
		jobState := manager.JobState{
			Stage: manager.JobStage_Queued,
			Ts:    time.Now(),
			Id:    uuid.New().String(),
		}
		var unmarshalErr *json.UnmarshalTypeError
		status := http.StatusOK
		message := "Success"

		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&jobState); err != nil {
			status = http.StatusBadRequest
			if errors.As(err, &unmarshalErr) {
				message = "jobHandler: wrong type for field " + unmarshalErr.Field
			} else {
				message = "jobHandler: bad request: " + err.Error()
			}
			return
		} else if err = jq.NewJob(&jobState); err != nil {
			status = http.StatusBadRequest
			message = "jobHandler: could not queue job: " + err.Error()
		}
		errorResponse(w, message, status)
		return
	}
}

func errorResponse(w http.ResponseWriter, message string, httpStatusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatusCode)
	resp := make(map[string]string)
	resp["message"] = message
	jsonResp, _ := json.Marshal(resp)
	w.Write(jsonResp)
}
