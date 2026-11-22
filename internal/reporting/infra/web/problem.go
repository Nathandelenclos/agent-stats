package web

import (
	"encoding/json"
	"net/http"
)

// problem is the body of every error response: an RFC 9457 problem document. Clients branch on Type, which is stable;
// Title and Detail are for people.
type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Parameter string `json:"parameter,omitempty"` // the query parameter at fault, for invalid-parameter
}

// The problem types the API answers with.
const (
	problemInvalidParameter = "urn:agent-stats:problem:invalid-parameter"
	problemHostNotAllowed   = "urn:agent-stats:problem:host-not-allowed"
	problemForeignOrigin    = "urn:agent-stats:problem:foreign-origin"
	problemMethodNotAllowed = "urn:agent-stats:problem:method-not-allowed"
	problemNotFound         = "urn:agent-stats:problem:not-found"
	problemInternal         = "urn:agent-stats:problem:internal"
)

func (p problem) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	json.NewEncoder(w).Encode(p)
}

func invalidParameter(name, detail string) problem {
	return problem{Type: problemInvalidParameter, Title: "Invalid query parameter", Status: http.StatusBadRequest, Detail: detail, Parameter: name}
}

// writeJSON answers 200 with v, encoded before anything is sent so that a failure still yields a clean error.
func writeJSON(w http.ResponseWriter, log logf, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log("encoding response: %v", err)
		problem{Type: problemInternal, Title: "Internal error", Status: http.StatusInternalServerError}.write(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(body, '\n'))
}
