package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/approval"
)

// ApprovalsHandler exposes the admin-facing side of durable approvals —
// what a console or a webhook-driven "approve"/"reject" link calls. Lives
// behind control-api, not the gateway: this is operator traffic, not the
// agent hot path.
type ApprovalsHandler struct {
	Manager *approval.Manager
}

type decideRequest struct {
	State     string `json:"state"`
	DecidedBy string `json:"decidedBy"`
}

func (h *ApprovalsHandler) Decide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req decideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.DecidedBy == "" {
		req.DecidedBy = "unknown"
	}
	if err := h.Manager.Decide(r.Context(), id, req.State, req.DecidedBy); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ApprovalsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := h.Manager.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, d)
}

func (h *ApprovalsHandler) List(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	list, err := h.Manager.List(r.Context(), state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, list)
}
