package sim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	contentTypeJSON = "application/json"
)

type HTTPHandler struct {
	sim *Simulator
	mux *http.ServeMux
}

func NewHTTPHandler(sim *Simulator) *HTTPHandler {
	h := &HTTPHandler{
		sim: sim,
		mux: http.NewServeMux(),
	}
	h.routes()
	return h
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *HTTPHandler) routes() {
	h.mux.HandleFunc("/healthz", h.handleHealthz)
	h.mux.HandleFunc("/metrics", h.handleMetrics)

	h.mux.HandleFunc("/v1/fleet/register", h.handleRegisterFleet)
	h.mux.HandleFunc("/v1/fleet/deregister", h.handleDeregisterFleet)
	h.mux.HandleFunc("/v1/allocate", h.handleAllocate)
	h.mux.HandleFunc("/v1/start", h.handleStart)
	h.mux.HandleFunc("/v1/runs/", h.handleRunStatus)
	h.mux.HandleFunc("/v1/workloads/", h.handleWorkloadAction)
}

type registerFleetResponse struct {
	OK bool `json:"ok"`
}

type registerFleetRequest struct {
	PoolKey string          `json:"poolKey"`
	Spec    GPUNodePoolSpec `json:"spec"`
}

func (h *HTTPHandler) handleRegisterFleet(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req registerFleetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.PoolKey == "" {
		req.PoolKey = DefaultPoolKey
	}
	if err := h.sim.RegisterFleet(req.PoolKey, req.Spec); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, registerFleetResponse{OK: true})
}

type deregisterFleetRequest struct {
	PoolKey string `json:"poolKey"`
}

func (h *HTTPHandler) handleDeregisterFleet(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req deregisterFleetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	h.sim.DeleteFleet(req.PoolKey)
	writeJSON(w, http.StatusOK, registerFleetResponse{OK: true})
}

type allocateRequest struct {
	WorkloadID string            `json:"workloadID"`
	GPUCount   int               `json:"gpuCount"`
	MemMiB     int               `json:"memMiB"`
	Options    AllocationOptions `json:"options"`
}

func (h *HTTPHandler) handleAllocate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req allocateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	alloc, err := h.sim.AllocateWithOptions(req.WorkloadID, req.GPUCount, req.MemMiB, req.Options)
	if err != nil {
		AllocationFailuresTotal.Inc()
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, alloc)
}

type startRequest struct {
	RuntimeInput
}

type startResponse struct {
	RunID string `json:"runID"`
}

func (h *HTTPHandler) handleStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req startRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runID, err := h.sim.StartWithRuntimeInput(req.RuntimeInput)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, startResponse{RunID: runID})
}

type runStatusResponse struct {
	RunID  string    `json:"runID"`
	Status RunStatus `json:"status"`
}

func (h *HTTPHandler) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	// Path shape: /v1/runs/{runID}/status
	path := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "status" {
		http.NotFound(w, r)
		return
	}
	runID := parts[0]

	status, err := h.sim.GetStatus(runID)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, runStatusResponse{
		RunID:  runID,
		Status: status,
	})
}

type actionResponse struct {
	OK bool `json:"ok"`
}

func (h *HTTPHandler) handleWorkloadAction(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	// Path shape: /v1/workloads/{workloadID}/{preempt|release}
	// workloadID may contain slashes (e.g. default/contention-wl-1), so take the last segment as action.
	path := strings.TrimPrefix(r.URL.Path, "/v1/workloads/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" {
		http.NotFound(w, r)
		return
	}
	action := parts[len(parts)-1]
	workloadID := strings.Join(parts[:len(parts)-1], "/")
	if workloadID == "" {
		http.NotFound(w, r)
		return
	}

	var err error
	switch action {
	case "preempt":
		err = h.sim.Preempt(workloadID)
	case "release":
		err = h.sim.Release(workloadID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, actionResponse{OK: true})
}

type healthResponse struct {
	Status string `json:"status"`
}

func (h *HTTPHandler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (h *HTTPHandler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	h.sim.mu.RLock()
	nodes := h.sim.allNodes()
	totalDevices := 0
	for _, n := range nodes {
		totalDevices += len(n.Devices)
	}
	allocatedDevices := 0
	for _, a := range h.sim.allocations {
		allocatedDevices += len(a.DeviceIDs)
	}
	h.sim.mu.RUnlock()

	DevicesTotalGauge.Set(float64(totalDevices))
	DevicesAllocatedGauge.Set(float64(allocatedDevices))

	promhttp.Handler().ServeHTTP(w, r)
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, errorResponse{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, dst any) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("invalid JSON body: multiple JSON values")
	}
	return nil
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method %s not allowed", r.Method))
		return false
	}
	return true
}

func statusForError(err error) int {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "already has an allocation") {
		return http.StatusConflict
	}
	if strings.Contains(msg, "already has a running run") {
		return http.StatusConflict
	}
	if strings.Contains(msg, "already exists") {
		return http.StatusConflict
	}
	if _, parseErr := strconv.Atoi(msg); parseErr == nil {
		return http.StatusBadRequest
	}
	return http.StatusBadRequest
}
