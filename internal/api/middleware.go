package api

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const companyKey contextKey = "company"

// authMiddleware extracts the Bearer token from the Authorization header,
// maps it to a company name, and stores the company in the request context.
// Returns 401 if the header is missing or the token is not recognized.
func (h *handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing Authorization header")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			writeError(w, http.StatusUnauthorized, "invalid Authorization header format")
			return
		}

		token := strings.TrimSpace(parts[1])
		company, ok := h.tokens[token]
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid or unrecognized token")
			return
		}

		ctx := context.WithValue(r.Context(), companyKey, company)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// companyFromContext retrieves the company name set by authMiddleware.
func companyFromContext(ctx context.Context) string {
	v, _ := ctx.Value(companyKey).(string)
	return v
}
