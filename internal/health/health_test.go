package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz_AlwaysReturns200(t *testing.T) {
	h := New()

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	h.Healthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body result
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
}

func TestHealthz_ContentType(t *testing.T) {
	h := New()
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	h.Healthz(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestReadyz_AllCheckersPass(t *testing.T) {
	h := New(
		Checker{Name: "database", Check: func(_ context.Context) error { return nil }},
		Checker{Name: "providers", Check: func(_ context.Context) error { return nil }},
	)

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()
	h.Readyz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body result
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	if body.Checks["database"] != "ok" {
		t.Errorf("database check = %q, want %q", body.Checks["database"], "ok")
	}
	if body.Checks["providers"] != "ok" {
		t.Errorf("providers check = %q, want %q", body.Checks["providers"], "ok")
	}
}

func TestReadyz_CheckerFails(t *testing.T) {
	h := New(
		Checker{Name: "database", Check: func(_ context.Context) error {
			return errors.New("connection refused")
		}},
		Checker{Name: "providers", Check: func(_ context.Context) error { return nil }},
	)

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()
	h.Readyz(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var body result
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if body.Status != "fail" {
		t.Errorf("status = %q, want %q", body.Status, "fail")
	}
	if body.Checks["database"] != "fail: connection refused" {
		t.Errorf("database check = %q, want %q", body.Checks["database"], "fail: connection refused")
	}
	if body.Checks["providers"] != "ok" {
		t.Errorf("providers check = %q, want %q", body.Checks["providers"], "ok")
	}
}

func TestReadyz_NoCheckers(t *testing.T) {
	h := New()

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()
	h.Readyz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body result
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
}

func TestReadyz_AllCheckersFail(t *testing.T) {
	h := New(
		Checker{Name: "database", Check: func(_ context.Context) error {
			return errors.New("timeout")
		}},
		Checker{Name: "providers", Check: func(_ context.Context) error {
			return errors.New("no providers configured")
		}},
	)

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()
	h.Readyz(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var body result
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if body.Status != "fail" {
		t.Errorf("status = %q, want %q", body.Status, "fail")
	}
	if body.Checks["database"] != "fail: timeout" {
		t.Errorf("database check = %q", body.Checks["database"])
	}
	if body.Checks["providers"] != "fail: no providers configured" {
		t.Errorf("providers check = %q", body.Checks["providers"])
	}
}

func TestRegister_RoutesWork(t *testing.T) {
	h := New(
		Checker{Name: "test", Check: func(_ context.Context) error { return nil }},
	)

	mux := http.NewServeMux()
	h.Register(mux)

	tests := []struct {
		path       string
		wantStatus int
	}{
		{"/healthz", http.StatusOK},
		{"/readyz", http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestReadyz_RespectsContextCancellation(t *testing.T) {
	h := New(
		Checker{Name: "slow", Check: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req := httptest.NewRequest("GET", "/readyz", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.Readyz(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
