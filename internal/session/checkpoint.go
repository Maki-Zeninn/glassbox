// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const checkpointFilename = "active_session.json"

// Checkpoint records the active debug session for crash-recovery purposes.
// It is written when a session starts and removed on clean exit. If the file
// still exists on the next invocation and the originating process is gone,
// the session was interrupted and can be recovered.
type Checkpoint struct {
	// SessionID is the ID of the session in the Store.
	SessionID string `json:"session_id"`
	// TxHash is the transaction being debugged.
	TxHash string `json:"tx_hash"`
	// Network is the Stellar network the session ran against.
	Network string `json:"network"`
	// StartedAt is the moment the session became active.
	StartedAt time.Time `json:"started_at"`
	// PID is the OS process ID of the Glassbox process that owns the session.
	PID int `json:"pid"`
}

// checkpointPath returns the path to the on-disk checkpoint file.
func checkpointPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory unavailable: %w", err)
	}
	return filepath.Join(home, ".Glassbox", checkpointFilename), nil
}

// WriteCheckpoint persists an active-session checkpoint for crash recovery.
// Call this when a debug session starts and ClearCheckpoint when it ends cleanly.
//
// The checkpoint fields are validated before writing so that incomplete or
// inconsistent checkpoints are rejected early with a clear diagnostic instead
// of silently creating a corrupt recovery file.
func WriteCheckpoint(cp *Checkpoint) error {
	if cp == nil {
		return fmt.Errorf("checkpoint must not be nil")
	}
	if cp.SessionID == "" {
		return fmt.Errorf(
			"checkpoint requires a session ID\n" +
				"  Fix: ensure the session is created before writing the checkpoint",
		)
	}
	if cp.TxHash == "" {
		return fmt.Errorf(
			"checkpoint requires a transaction hash\n" +
				"  Fix: provide the transaction hash when starting the debug session",
		)
	}
	if cp.Network == "" {
		return fmt.Errorf(
			"checkpoint requires a network\n" +
				"  Fix: provide the network when starting the debug session",
		)
	}
	validNetworks := map[string]bool{"testnet": true, "mainnet": true, "futurenet": true}
	if !validNetworks[cp.Network] {
		return fmt.Errorf(
			"unsupported network %q in checkpoint — must be one of: testnet, mainnet, futurenet\n"+
				"  Fix: use a valid network when starting the debug session",
			cp.Network,
		)
	}
	if cp.StartedAt.IsZero() {
		cp.StartedAt = time.Now()
	}

	path, err := checkpointPath()
	if err != nil {
		return err
	}
	cp.PID = os.Getpid()
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}
	return writeFileAtomic(path, data, 0600)
}

// ClearCheckpoint removes the crash-recovery checkpoint after a clean session exit.
// A missing checkpoint file is not treated as an error.
func ClearCheckpoint() error {
	path, err := checkpointPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove checkpoint: %w", err)
	}
	return nil
}

// LoadCheckpoint reads the last crash-recovery checkpoint if one exists.
// Returns (nil, nil) when no checkpoint file is present.
func LoadCheckpoint() (*Checkpoint, error) {
	path, err := checkpointPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("malformed checkpoint file: %w", err)
	}
	return &cp, nil
}

// IsOrphaned returns true when the checkpoint's originating process is no
// longer running, indicating an unclean termination that left the session open.
// It uses signal 0 (existence probe) which never delivers a signal to the target.
func (c *Checkpoint) IsOrphaned() bool {
	if c.PID <= 0 {
		return true
	}
	return !processAlive(c.PID)
}

// CheckpointIssue describes a single problem found during checkpoint validation.
type CheckpointIssue struct {
	// Field is the checkpoint field that is invalid or missing.
	Field string
	// Description explains what is wrong.
	Description string
	// Hint is an optional actionable suggestion for the user.
	Hint string
}

// CheckpointReport is the output of ValidateCheckpoint.
type CheckpointReport struct {
	// OK is true when no issues were found.
	OK bool
	// Issues lists every validation problem found.
	Issues []CheckpointIssue
}

// ValidateCheckpoint checks a Checkpoint record for completeness and
// consistency before it is trusted for crash-recovery purposes. It validates:
//
//   - SessionID is non-empty
//   - TxHash is non-empty
//   - Network is a recognised Stellar network value
//   - StartedAt is non-zero
//   - PID is positive
//
// The function never modifies the checkpoint and is safe to call concurrently.
func ValidateCheckpoint(cp *Checkpoint) *CheckpointReport {
	report := &CheckpointReport{}

	if cp.SessionID == "" {
		report.Issues = append(report.Issues, CheckpointIssue{
			Field:       "SessionID",
			Description: "checkpoint is missing the session ID",
			Hint:        "The checkpoint file is corrupt. Delete ~/.Glassbox/active_session.json and re-run 'glassbox debug <tx-hash>'.",
		})
	}

	if cp.TxHash == "" {
		report.Issues = append(report.Issues, CheckpointIssue{
			Field:       "TxHash",
			Description: "checkpoint is missing the transaction hash",
			Hint:        "Re-run 'glassbox debug <tx-hash>' to create a valid checkpoint.",
		})
	}

	if cp.Network == "" {
		report.Issues = append(report.Issues, CheckpointIssue{
			Field:       "Network",
			Description: "checkpoint is missing the network",
			Hint:        "Re-run 'glassbox debug <tx-hash> --network <testnet|mainnet|futurenet>'.",
		})
	} else {
		validNetworks := map[string]bool{
			"testnet": true, "mainnet": true, "futurenet": true,
		}
		if !validNetworks[cp.Network] {
			report.Issues = append(report.Issues, CheckpointIssue{
				Field:       "Network",
				Description: "checkpoint network value " + cp.Network + " is not a recognised Stellar network",
				Hint:        "Accepted values are: testnet, mainnet, futurenet.",
			})
		}
	}

	if cp.StartedAt.IsZero() {
		report.Issues = append(report.Issues, CheckpointIssue{
			Field:       "StartedAt",
			Description: "checkpoint has a zero started_at timestamp",
			Hint:        "The checkpoint was written by an incompatible Glassbox version. Delete it and re-run 'glassbox debug'.",
		})
	}

	if cp.PID <= 0 {
		report.Issues = append(report.Issues, CheckpointIssue{
			Field:       "PID",
			Description: fmt.Sprintf("checkpoint has an invalid PID: %d", cp.PID),
			Hint:        "The checkpoint was created by an incompatible Glassbox version. Delete ~/.Glassbox/active_session.json.",
		})
	}

	report.OK = len(report.Issues) == 0
	return report
}
