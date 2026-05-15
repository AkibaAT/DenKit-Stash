package handlers

import (
	"encoding/json"
	"net/http"
)

// GET /wharf/status - Check wharf infrastructure status
func (h *WharfHandlers) GetWharfStatus(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"ok": true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
