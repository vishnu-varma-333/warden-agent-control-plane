package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/policy"
)

// PoliciesHandler exposes Cedar policy versions to the console's policies
// view: list every version, write a new one (validated, never active on
// creation), and activate one.
type PoliciesHandler struct {
	Engine *policy.Engine
}

func (h *PoliciesHandler) List(w http.ResponseWriter, r *http.Request) {
	versions, err := h.Engine.ListVersions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, versions)
}

type createPolicyRequest struct {
	CedarSource string `json:"cedarSource"`
}

func (h *PoliciesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	version, err := h.Engine.CreateVersion(r.Context(), req.CedarSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]int{"version": version})
}

func (h *PoliciesHandler) Activate(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		http.Error(w, "invalid version", http.StatusBadRequest)
		return
	}
	if err := h.Engine.Activate(r.Context(), version); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
