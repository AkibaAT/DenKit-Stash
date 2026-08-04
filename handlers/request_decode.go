package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func decodeJSONOrFormRequest(w http.ResponseWriter, r *http.Request, dest interface{}, assignForm func() error) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes()))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "could not read request body")
		return false
	}

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err = json.Unmarshal(body, dest); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
			return false
		}
		return true
	}

	r.Body = io.NopCloser(strings.NewReader(string(body)))
	if err = r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid form data: %s", err.Error()))
		return false
	}
	if assignForm != nil {
		if err = assignForm(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return false
		}
	}
	return true
}
