package v1

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"time"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// tlsStatusDTO is the read view of TLS config. It never returns the PEMs — the
// private key is secret, and the cert is summarised (subject + expiry) so the
// admin can confirm what's installed without re-downloading it.
type tlsStatusDTO struct {
	Configured bool   `json:"configured"`          // serving HTTPS
	Source     string `json:"source"`              // "env-file" | "uploaded" | "none"
	Subject    string `json:"subject,omitempty"`   // leaf cert CN (uploaded only)
	NotAfter   string `json:"not_after,omitempty"` // leaf cert expiry, RFC3339 (uploaded only)
}

// GetTLS handles GET /settings/tls — the current TLS status (no secrets).
func (h *SettingsHandler) GetTLS(w http.ResponseWriter, r *http.Request) {
	dto := tlsStatusDTO{Source: "none"}
	if h.tlsEnvConfigured {
		// Env file paths win and are managed outside the UI.
		dto.Configured = true
		dto.Source = "env-file"
		respond.Success(w, r, dto)
		return
	}
	tc := h.svc.TLS(r.Context())
	if tc.CertPEM != "" && tc.KeyPEM != "" {
		dto.Configured = true
		dto.Source = "uploaded"
		if leaf := parseLeafCert(tc.CertPEM); leaf != nil {
			dto.Subject = leaf.Subject.CommonName
			dto.NotAfter = leaf.NotAfter.UTC().Format(time.RFC3339)
		}
	}
	respond.Success(w, r, dto)
}

// tlsUpdateDTO is the upload shape. Both empty clears the stored cert (revert to
// plain HTTP on the next restart).
type tlsUpdateDTO struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// UpdateTLS handles PUT /settings/tls — validate the pair, then store encrypted.
// Restart-required (the listener binds at startup).
func (h *SettingsHandler) UpdateTLS(w http.ResponseWriter, r *http.Request) {
	if h.tlsEnvConfigured {
		respond.BadRequest(w, r, "TLS is set via TLS_CERT_FILE/TLS_KEY_FILE; unset those env vars to manage the certificate here")
		return
	}
	var dto tlsUpdateDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	// Reject a half-set pair, and validate that a supplied pair actually matches
	// before storing — a bad cert that only fails at ListenAndServeTLS would take
	// the server down on its next restart.
	if (dto.CertPEM == "") != (dto.KeyPEM == "") {
		respond.BadRequest(w, r, "provide both the certificate and the private key, or neither to clear")
		return
	}
	if dto.CertPEM != "" {
		if _, err := tls.X509KeyPair([]byte(dto.CertPEM), []byte(dto.KeyPEM)); err != nil {
			respond.BadRequest(w, r, "certificate and key are not a valid pair: "+err.Error())
			return
		}
	}
	if err := h.svc.SetTLS(ctx, settings.TLSConfig{CertPEM: dto.CertPEM, KeyPEM: dto.KeyPEM}); err != nil {
		h.logger.ErrorContext(ctx, "update tls config", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			action := "uploaded"
			if dto.CertPEM == "" {
				action = "cleared"
			}
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{"tls": action}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}

// parseLeafCert decodes the first PEM block as an X.509 cert, or nil on failure.
func parseLeafCert(certPEM string) *x509.Certificate {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return leaf
}
