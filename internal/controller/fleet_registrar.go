package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ishanchopra/gpu-scheduler/internal/sim"
)

// MultiFleetRegistrar updates the local in-process simulator used by the
// scheduler and, optionally, propagates the same fleet spec to the external
// simulator HTTP service used by workers.
type MultiFleetRegistrar struct {
	Local        *sim.Simulator
	RemoteBase   string
	StrictRemote bool // when true, remote POST failure returns an error (reconciliation retries)
	httpClient   *http.Client
}

// NewMultiFleetRegistrar creates a registrar that always updates the local
// simulator and, when remoteBase is non-empty, also POSTs the fleet spec to
// <remoteBase>/v1/fleet/register. When strictRemote is true, a failed remote
// POST causes RegisterFleet to return an error so reconciliation is retried
// until the simulator is available (e.g. full-pipeline e2e).
func NewMultiFleetRegistrar(local *sim.Simulator, remoteBase string, strictRemote bool) *MultiFleetRegistrar {
	return &MultiFleetRegistrar{
		Local:        local,
		RemoteBase:   strings.TrimSuffix(remoteBase, "/"),
		StrictRemote: strictRemote,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type registerFleetRequest struct {
	PoolKey string              `json:"poolKey"`
	Spec    sim.GPUNodePoolSpec `json:"spec"`
}

// RegisterFleet first updates the local simulator, then mirrors the pool to
// the external simulator HTTP API (if configured). poolKey uniquely identifies
// the pool (e.g. namespace/name of the GPUNodePool CR).
func (m *MultiFleetRegistrar) RegisterFleet(poolKey string, spec sim.GPUNodePoolSpec) error {
	if m.Local != nil {
		if err := m.Local.RegisterFleet(poolKey, spec); err != nil {
			return err
		}
	}
	if m.RemoteBase == "" {
		return nil
	}

	body, err := json.Marshal(registerFleetRequest{PoolKey: poolKey, Spec: spec})
	if err != nil {
		return err
	}

	resp, err := m.httpClient.Post(m.RemoteBase+"/v1/fleet/register", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("RegisterFleet: remote simulator unreachable (%s): %v", m.RemoteBase, err)
		if m.StrictRemote {
			return fmt.Errorf("remote simulator unreachable (%s): %w", m.RemoteBase, err)
		}
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	var errResp struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	msg := resp.Status
	if errResp.Error != "" {
		msg = fmt.Sprintf("%s: %s", resp.Status, errResp.Error)
	}
	log.Printf("RegisterFleet: remote simulator returned %s (fleet still registered locally)", msg)
	if m.StrictRemote {
		return fmt.Errorf("remote simulator returned %s", msg)
	}
	return nil
}

// DeleteFleet removes the pool from the local simulator and, when remote is
// configured, from the remote simulator so worker allocations stay consistent.
func (m *MultiFleetRegistrar) DeleteFleet(poolKey string) {
	if m.Local != nil {
		m.Local.DeleteFleet(poolKey)
	}
	if m.RemoteBase == "" || poolKey == "" {
		return
	}
	body, err := json.Marshal(struct {
		PoolKey string `json:"poolKey"`
	}{PoolKey: poolKey})
	if err != nil {
		return
	}
	req, _ := http.NewRequest(http.MethodPost, m.RemoteBase+"/v1/fleet/deregister", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		log.Printf("DeleteFleet: remote deregister (%s): %v", m.RemoteBase, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		log.Printf("DeleteFleet: remote returned %s", resp.Status)
	}
}

// FleetUsageForPool returns utilization for the given pool from the local simulator.
func (m *MultiFleetRegistrar) FleetUsageForPool(poolKey string) (sim.FleetUsage, bool) {
	if m.Local == nil {
		return sim.FleetUsage{}, false
	}
	return m.Local.FleetUsageForPool(poolKey)
}
