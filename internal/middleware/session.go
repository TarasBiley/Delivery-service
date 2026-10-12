package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey string

const sessionIDKey contextKey = "session_id"

func Session(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			sessionID := ""

			cookie, err := r.Cookie("session_id")
			if err == nil {
				if parsedID, parseErr := uuid.Parse(cookie.Value); parseErr == nil {
					sessionID = parsedID.String()
				}
			}

			if sessionID == "" {
				sessionID = uuid.NewString()

				http.SetCookie(w, &http.Cookie{
					Name:     "session_id",
					Value:    sessionID,
					Path:     "/",
					HttpOnly: true,
					Secure:   secure,
					SameSite: http.SameSiteLaxMode,
				})
			}

			ctx := context.WithValue(
				r.Context(),
				sessionIDKey,
				sessionID,
			)

			next.ServeHTTP(
				w,
				r.WithContext(ctx),
			)
		})
	}
}

func SessionID(ctx context.Context) string {
	sessionID, _ := ctx.Value(sessionIDKey).(string)

	return sessionID
}
