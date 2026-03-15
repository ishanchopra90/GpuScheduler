package sim

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPHandler_HealthzAndMetrics(t *testing.T) {
	s := NewSimulator()
	h := NewHTTPHandler(s)

	t.Run("healthz", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("content-type = %q, want application/json", ct)
		}
	})

	t.Run("metrics", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "sim_devices_total") {
			t.Fatalf("missing sim_devices_total in metrics body: %q", body)
		}
		if !strings.Contains(body, "sim_devices_allocated") {
			t.Fatalf("missing sim_devices_allocated in metrics body: %q", body)
		}
	})
}

func TestHTTPHandler_SimulatorLifecycle(t *testing.T) {
	s := NewSimulator()
	h := NewHTTPHandler(s)

	mustPOSTJSON(t, h, "/v1/fleet/register", map[string]any{
		"poolKey": DefaultPoolKey,
		"spec": GPUNodePoolSpec{
			NodeCount:          2,
			DevicesPerNode:     2,
			MemoryMiBPerDevice: 16000,
			DeviceType:         "nvidia",
			Profile:            "h100_sxm",
		},
	})

	var alloc Allocation
	mustPOSTJSONInto(t, h, "/v1/allocate", map[string]any{
		"workloadID": "w1",
		"gpuCount":   2,
		"memMiB":     1000,
		"options": map[string]any{
			"placementPolicy": "first_fit",
		},
	}, &alloc)
	if alloc.WorkloadID != "w1" || len(alloc.DeviceIDs) != 2 {
		t.Fatalf("unexpected allocation: %#v", alloc)
	}

	var startResp struct {
		RunID string `json:"runID"`
	}
	mustPOSTJSONInto(t, h, "/v1/start", RuntimeInput{
		WorkloadID: "w1",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindTraining,
	}, &startResp)
	if startResp.RunID == "" {
		t.Fatalf("expected non-empty runID")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+startResp.RunID+"/status", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var stResp struct {
		Status RunStatus `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stResp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if stResp.Status != RunStatusRunning {
		t.Fatalf("status = %q, want %q", stResp.Status, RunStatusRunning)
	}

	// Fast-forward the simulated run so the next status poll observes completion.
	s.mu.Lock()
	s.runs[startResp.RunID].EndsAt = time.Now().Add(-time.Second)
	s.mu.Unlock()

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+startResp.RunID+"/status", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stResp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if stResp.Status != RunStatusSucceeded {
		t.Fatalf("status = %q, want %q", stResp.Status, RunStatusSucceeded)
	}

	mustPOSTJSON(t, h, "/v1/workloads/w1/release", map[string]any{})
}

func TestHTTPHandler_Preempt(t *testing.T) {
	s := NewSimulator()
	h := NewHTTPHandler(s)

	mustPOSTJSON(t, h, "/v1/fleet/register", map[string]any{
		"poolKey": DefaultPoolKey,
		"spec": GPUNodePoolSpec{
			NodeCount:          1,
			DevicesPerNode:     1,
			MemoryMiBPerDevice: 16000,
			Profile:            "h100_sxm",
		},
	})
	mustPOSTJSON(t, h, "/v1/allocate", map[string]any{
		"workloadID": "w1",
		"gpuCount":   1,
		"memMiB":     1000,
	})
	var startResp struct {
		RunID string `json:"runID"`
	}
	mustPOSTJSONInto(t, h, "/v1/start", RuntimeInput{
		WorkloadID: "w1",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindTraining,
	}, &startResp)

	mustPOSTJSON(t, h, "/v1/workloads/w1/preempt", map[string]any{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+startResp.RunID+"/status", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var stResp struct {
		Status RunStatus `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stResp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if stResp.Status != RunStatusPreempted {
		t.Fatalf("status = %q, want %q", stResp.Status, RunStatusPreempted)
	}
}

func TestHTTPHandler_MethodValidation(t *testing.T) {
	s := NewSimulator()
	h := NewHTTPHandler(s)

	req := httptest.NewRequest(http.MethodGet, "/v1/fleet/register", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func mustPOSTJSON(t *testing.T, h http.Handler, path string, body any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, toJSONBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s status = %d, want %d, body=%s", path, rec.Code, http.StatusOK, rec.Body.String())
	}
}

func mustPOSTJSONInto(t *testing.T, h http.Handler, path string, body any, out any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, toJSONBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s status = %d, want %d, body=%s", path, rec.Code, http.StatusOK, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode response for %s: %v body=%s", path, err, rec.Body.String())
	}
}

func toJSONBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return bytes.NewReader(b)
}
