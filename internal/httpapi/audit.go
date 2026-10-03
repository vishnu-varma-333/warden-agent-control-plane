package httpapi

import (
	"crypto/ed25519"
	"database/sql"
	"net/http"
	"strconv"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/audit"
)

// AuditHandler exposes the hash-chained audit log to the console's audit
// view: browsing recent decisions, and running the same verification
// wardenctl runs from the command line, on demand, from a button.
type AuditHandler struct {
	DB          *sql.DB
	AuditPubKey ed25519.PublicKey // nil: verify always falls back to full-chain verification
}

func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	records, err := audit.ListRecent(r.Context(), h.DB, limit, before)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, records)
}

func (h *AuditHandler) Verify(w http.ResponseWriter, r *http.Request) {
	var result audit.VerifyResult
	var err error
	if h.AuditPubKey != nil && r.URL.Query().Get("fromCheckpoint") == "true" {
		result, err = audit.VerifyFromLatestCheckpoint(r.Context(), h.DB, h.AuditPubKey)
	} else {
		result, err = audit.VerifyFull(r.Context(), h.DB)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}
