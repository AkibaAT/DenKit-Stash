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
			http.Error(w, `{"errors":["request body too large"]}`, http.StatusRequestEntityTooLarge)
			return false
		}
		fmt.Printf("Error reading request body: %v\n", err)
		http.Error(w, `{"errors":["could not read request body"]}`, http.StatusBadRequest)
		return false
	}

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err = json.Unmarshal(body, dest); err != nil {
			fmt.Printf("JSON parsing error: %v\n", err)
			http.Error(w, fmt.Sprintf(`{"errors":["invalid request body: %s"]}`, err.Error()), http.StatusBadRequest)
			return false
		}
		return true
	}

	r.Body = io.NopCloser(strings.NewReader(string(body)))
	if err = r.ParseForm(); err != nil {
		fmt.Printf("Form parsing error: %v\n", err)
		http.Error(w, fmt.Sprintf(`{"errors":["invalid form data: %s"]}`, err.Error()), http.StatusBadRequest)
		return false
	}
	if assignForm != nil {
		if err = assignForm(); err != nil {
			http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusBadRequest)
			return false
		}
	}
	return true
}
