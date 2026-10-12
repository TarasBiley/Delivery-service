package middleware

import (
	"context"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSession(t *testing.T) {
	existing := uuid.NewString()
	for _, secure := range []bool{false, true} {
		for _, tc := range []struct {
			name, value string
			reuse       bool
		}{{"missing", "", false}, {"invalid", "bad-id", false}, {"valid", existing, true}, {"normalized", strings.ToUpper(existing), true}} {
			t.Run(tc.name+map[bool]string{false: "/http", true: "/https"}[secure], func(t *testing.T) {
				req := httptest.NewRequest("GET", "/", nil)
				if tc.name != "missing" {
					req.AddCookie(&http.Cookie{Name: "session_id", Value: tc.value})
				}
				rec := httptest.NewRecorder()
				var id string
				Session(secure)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { id = SessionID(r.Context()) })).ServeHTTP(rec, req)
				parsed, err := uuid.Parse(id)
				if err != nil {
					t.Fatal(err)
				}
				cookies := rec.Result().Cookies()
				if tc.reuse {
					if id != existing || len(cookies) != 0 {
						t.Fatalf("reused id=%s cookies=%v", id, cookies)
					}
					return
				}
				if parsed.Version() != 4 || len(cookies) != 1 {
					t.Fatalf("new id=%s cookies=%v", id, cookies)
				}
				c := cookies[0]
				if c.Name != "session_id" || c.Value != id || c.Path != "/" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure != secure {
					t.Fatalf("cookie=%+v", c)
				}
			})
		}
	}
	if SessionID(context.Background()) != "" {
		t.Fatal("unexpected session in empty context")
	}
}
