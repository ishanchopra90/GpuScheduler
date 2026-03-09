package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ishanchopra/gpu-scheduler/internal/sim"
)

//go:generate mockgen -source=simclient.go -destination=../../mocks/mock_simulator_client.go -package=mocks

// SimulatorClient is the interface for calling the simulator (allocate, start, run status, release).
// Use this in callers so implementations can be mocked in unit tests.
// profile is the workload's requested hardware profile (e.g. h100_sxm); used for multi-pool fleets.
type SimulatorClient interface {
	Allocate(workloadID string, gpuCount, memMiB int, profile string) error
	Start(input sim.RuntimeInput) (runID string, err error)
	GetRunStatus(runID string) (sim.RunStatus, error)
	// Release frees the workload's allocation in the simulator so devices return to the pool.
	// Call after a run completes (Succeeded, Failed, or Preempted) so other workloads can use the devices.
	Release(workloadID string) error
}

// HTTPSimClient calls the simulator HTTP API.
type HTTPSimClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewSimClient returns a client that talks to the simulator at baseURL (e.g. http://simulator:8080).
func NewSimClient(baseURL string) SimulatorClient {
	return &HTTPSimClient{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Ensure HTTPSimClient implements SimulatorClient.
var _ SimulatorClient = (*HTTPSimClient)(nil)

type allocateReq struct {
	WorkloadID string                `json:"workloadID"`
	GPUCount   int                   `json:"gpuCount"`
	MemMiB     int                   `json:"memMiB"`
	Options    sim.AllocationOptions `json:"options"`
}

// Allocate reserves devices in the simulator. profile selects devices from the matching pool in multi-profile fleets.
// Idempotent if workload already has an allocation (returns nil).
func (c *HTTPSimClient) Allocate(workloadID string, gpuCount, memMiB int, profile string) error {
	opts := sim.AllocationOptions{PlacementPolicy: sim.PlacementPolicyFirstFit, PreferredProfile: strings.ToLower(strings.TrimSpace(profile))}
	body, _ := json.Marshal(allocateReq{
		WorkloadID: workloadID,
		GPUCount:   gpuCount,
		MemMiB:     memMiB,
		Options:    opts,
	})
	resp, err := c.HTTPClient.Post(c.BaseURL+"/v1/allocate", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusConflict {
		return nil
	}
	var errResp struct{ Error string }
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp.Error != "" {
		return fmt.Errorf("allocate %s: %s", resp.Status, errResp.Error)
	}
	return fmt.Errorf("allocate %s", resp.Status)
}

type startReq struct {
	sim.RuntimeInput
}

type startResp struct {
	RunID string `json:"runID"`
}

// Start begins execution in the simulator and returns the runID for status polling.
func (c *HTTPSimClient) Start(input sim.RuntimeInput) (runID string, err error) {
	body, err := json.Marshal(startReq{RuntimeInput: input})
	if err != nil {
		return "", err
	}
	resp, err := c.HTTPClient.Post(c.BaseURL+"/v1/start", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResp struct{ Error string }
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error != "" {
			return "", fmt.Errorf("start %s: %s", resp.Status, errResp.Error)
		}
		return "", fmt.Errorf("start %s", resp.Status)
	}
	var out startResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.RunID, nil
}

type runStatusResp struct {
	RunID  string        `json:"runID"`
	Status sim.RunStatus `json:"status"`
}

// GetRunStatus returns the status of a run. Use the runID from Start.
func (c *HTTPSimClient) GetRunStatus(runID string) (sim.RunStatus, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/v1/runs/" + runID + "/status")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResp struct{ Error string }
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error != "" {
			return "", fmt.Errorf("get status %s: %s", resp.Status, errResp.Error)
		}
		return "", fmt.Errorf("get status %s", resp.Status)
	}
	var out runStatusResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Status, nil
}

// Release frees the workload's allocation in the remote simulator (POST /v1/workloads/{workloadID}/release).
// Call after a run completes so devices return to the pool for other workloads.
func (c *HTTPSimClient) Release(workloadID string) error {
	if workloadID == "" {
		return fmt.Errorf("workloadID is required")
	}
	path := c.BaseURL + "/v1/workloads/" + workloadID + "/release"
	resp, err := c.HTTPClient.Post(path, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var errResp struct{ Error string }
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp.Error != "" {
		return fmt.Errorf("release %s: %s", resp.Status, errResp.Error)
	}
	return fmt.Errorf("release %s", resp.Status)
}
