// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/version"
	_ "modernc.org/sqlite"
)

const (
	// SchemaVersion tracks the database schema version for migrations
	SchemaVersion = 2

	// DefaultTTL is the default time-to-live for sessions (30 days)
	DefaultTTL = 30 * 24 * time.Hour

	// DefaultMaxSessions is the maximum number of sessions to keep
	DefaultMaxSessions = 1000
)

// Data represents the complete state of a debug session
type Data struct {
	ID            string    `json:"id"`
	Name          string    `json:"name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastAccessAt  time.Time `json:"last_access_at"`
	Status        string    `json:"status"` // active, saved, resumed, recovered, expired
	Network       string    `json:"network"`
	HorizonURL    string    `json:"horizon_url"`
	TxHash        string    `json:"tx_hash"`
	EnvelopeXdr   string    `json:"envelope_xdr"`
	ResultXdr     string    `json:"result_xdr"`
	ResultMetaXdr string    `json:"result_meta_xdr"`
	PinnedEndpoint string   `json:"pinned_endpoint,omitempty"`

	// Audit Chain Integrity [Issue #330]
	AuditHash           string `json:"audit_hash,omitempty"`            // SHA-256 of the session payload
	AuditSignature      string `json:"audit_signature,omitempty"`       // Ed25519 signature of the AuditHash
	PreviousSessionHash string `json:"previous_session_hash,omitempty"` // AuditHash of the predecessor session

	// Simulator I/O
	SimRequestJSON  string `json:"sim_request_json"`  // JSON sent to glassbox-sim
	SimResponseJSON string `json:"sim_response_json"` // JSON received from glassbox-sim

	// Metadata
	ErstVersion   string `json:"GLASSBOX_version"`
	EnvFingerprint string `json:"env_fingerprint,omitempty"`
	SchemaVersion int    `json:"schema_version"`

	// Session provenance timeline [Issue #59]. Serialized ProvenanceTimeline
	// JSON; append-only and bounded (see provenance.go).
	ProvenanceJSON string `json:"provenance_json,omitempty"`

	// User annotations attached to this session [Issue #58]. Persisted to
	// the store like any other field but excluded from session.json
	// (json:"-") so it forms its own independent, separately hashed
	// "annotations" member in a session archive's integrity Manifest (see
	// manifest.go). ImportSession's merge policy combines this field
	// across colliding sessions instead of overwriting it.
	AnnotationsJSON string `json:"-"`

	// Archive-only artifacts [Issue #56]. Unlike AnnotationsJSON, these are
	// never persisted to the SQLite store — they exist only as independent
	// members of a shared session archive, each hashed separately in the
	// archive's integrity Manifest. ExportArchive writes them as their own
	// zip entries; ImportArchive repopulates them after verifying the
	// manifest.
	TraceJSON     string `json:"-"`
	BundleJSON    string `json:"-"`
	SourceMapJSON string `json:"-"`
}

// Store manages session persistence in SQLite
type Store struct {
	db *sql.DB
}

// DefaultDBPath returns the filesystem path where the session database is stored.
// It is safe to call without opening the store.
func DefaultDBPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "~/.Glassbox/sessions.db"
	}
	return filepath.Join(homeDir, ".Glassbox", "sessions.db")
}

// NewStore creates or opens the session database in the default location
// (~/.Glassbox/sessions.db).
func NewStore() (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	return NewStoreAt(filepath.Join(homeDir, ".Glassbox"))
}

// NewStoreAt creates or opens the session database under glassboxDir instead
// of the default ~/.Glassbox. It exists for tooling that needs to operate on
// a non-default data directory, such as 'glassbox session gc --root'.
func NewStoreAt(glassboxDir string) (*Store, error) {
	erstDir := glassboxDir
	if err := os.MkdirAll(erstDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .Glassbox directory: %s",
			SanitizeErrorMessage(err.Error()))
	}

	dbPath := filepath.Join(erstDir, "sessions.db")

	// Validate DB permissions before opening so we surface a clear, PII-free
	// error rather than a raw sqlite driver error that may contain path details.
	if permErr := ValidateDBPermissions(dbPath); permErr != nil {
		return nil, permErr
	}

	// Open SQLite database
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, WrapStoreError("open", dbPath, err)
	}

	store := &Store{db: db}

	// Initialize schema
	if err = store.initSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Set file permissions to 600 (read/write for owner only)
	if chmodErr := os.Chmod(dbPath, 0600); chmodErr != nil {
		logger.Logger.Warn("Failed to set database permissions",
			"error", SanitizeErrorMessage(chmodErr.Error()))
	}

	return store, nil
}

// initSchema creates the sessions table if it doesn't exist
func (s *Store) initSchema() error {
	query := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		name TEXT,
		created_at TIMESTAMP NOT NULL,
		last_access_at TIMESTAMP NOT NULL,
		status TEXT NOT NULL,
		network TEXT NOT NULL,
		horizon_url TEXT NOT NULL,
		tx_hash TEXT NOT NULL,
		envelope_xdr TEXT,
		result_xdr TEXT,
		result_meta_xdr TEXT,
		pinned_endpoint TEXT,
		audit_hash TEXT,
		audit_signature TEXT,
		previous_session_hash TEXT,
		sim_request_json TEXT,
		sim_response_json TEXT,
		env_fingerprint TEXT,
		provenance_json TEXT,
		annotations_json TEXT,
		GLASSBOX_version TEXT,
		schema_version INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_last_access ON sessions(last_access_at);
	CREATE INDEX IF NOT EXISTS idx_tx_hash ON sessions(tx_hash);
	CREATE INDEX IF NOT EXISTS idx_audit_hash ON sessions(audit_hash);
	`

	if _, err := s.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	if err := s.ensureColumn("sessions", "name", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("sessions", "pinned_endpoint", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("sessions", "env_fingerprint", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("sessions", "provenance_json", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("sessions", "annotations_json", "TEXT"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_session_name ON sessions(name) WHERE name IS NOT NULL AND name != ''`); err != nil {
		return fmt.Errorf("failed to create session name index: %w", err)
	}

	// Schema migrations for existing databases
	if err := s.ensureColumn("sessions", "pinned_endpoint", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("sessions", "audit_hash", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("sessions", "audit_signature", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("sessions", "previous_session_hash", "TEXT"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(table, column, definition string) error {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("failed to inspect %s schema: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("failed to scan %s schema: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("failed to add %s.%s column: %w", table, column, err)
	}
	return nil
}

// SaveWithValidation validates the session data for integrity before persisting
// it. It returns a descriptive error listing all issues found, so callers
// receive actionable feedback instead of a silent partial-write.
//
// Use this method in preference to Save when the caller cannot guarantee
// that the Data has already been validated (e.g. external imports, recovery
// paths, or data loaded from archives).
func (s *Store) SaveWithValidation(ctx context.Context, data *Data) error {
	if data == nil {
		return fmt.Errorf(
			"cannot save nil session data\n" +
				"  Fix: run 'glassbox debug <tx-hash>' to create a session before saving",
		)
	}

	report := ValidateIntegrity(data)
	if !report.OK {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("session data failed validation (%d issue(s)):\n", len(report.Issues)))
		for i, issue := range report.Issues {
			sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, issue.Field, issue.Description))
			if issue.Hint != "" {
				sb.WriteString(fmt.Sprintf("     Hint: %s\n", issue.Hint))
			}
		}
		return fmt.Errorf("%s", sb.String())
	}

	return s.Save(ctx, data)
}

// Save persists a session to the database after validating required fields.
// It performs inline validation equivalent to SaveWithValidation but without
// the full integrity-report formatting. Prefer SaveWithValidation when the
// caller cannot guarantee the Data has already been validated externally.
func (s *Store) Save(ctx context.Context, data *Data) error {
	if data.ID == "" {
		return fmt.Errorf("session ID is required")
	}
	if data.TxHash == "" {
		return fmt.Errorf(
			"session transaction hash is required\n" +
				"  Fix: run 'glassbox debug <tx-hash>' to create a session with a valid transaction hash",
		)
	}
	if data.Network == "" {
		return fmt.Errorf(
			"session network is required\n" +
				"  Fix: provide a network with --network testnet (or mainnet, futurenet)",
		)
	}
	validNetworks := map[string]bool{"testnet": true, "mainnet": true, "futurenet": true}
	if !validNetworks[data.Network] {
		return fmt.Errorf(
			"unsupported network %q — must be one of: testnet, mainnet, futurenet\n"+
				"  Fix: re-run with --network testnet (or mainnet, futurenet)",
			data.Network,
		)
	}
	if data.Status == "" {
		data.Status = "active"
	}
	validStatuses := map[string]bool{
		"active": true, "saved": true, "resumed": true,
		"recovered": true, "expired": true,
	}
	if !validStatuses[data.Status] {
		return fmt.Errorf(
			"invalid session status %q — must be one of: active, saved, resumed, recovered, expired\n"+
				"  Fix: set a valid status when creating the session",
			data.Status,
		)
	}
	if data.HorizonURL == "" && data.Network != "" {
		// Auto-populate known Horizon URLs for convenience.
		switch data.Network {
		case "testnet":
			data.HorizonURL = "https://horizon-testnet.stellar.org"
		case "mainnet":
			data.HorizonURL = "https://horizon.stellar.org"
		case "futurenet":
			data.HorizonURL = "https://horizon-futurenet.stellar.org"
		}
	}
	if data.Name != "" && len(data.Name) > 128 {
		return fmt.Errorf(
			"session name is too long (%d characters, max 128)\n"+
				"  Fix: provide a shorter name with --name",
			len(data.Name),
		)
	}

	now := time.Now()
	if data.CreatedAt.IsZero() {
		data.CreatedAt = now
	}
	data.LastAccessAt = now
	data.SchemaVersion = SchemaVersion
	data.ErstVersion = version.Version

	if data.EnvFingerprint == "" {
		data.EnvFingerprint = BuildEnvFingerprint()
	}

	query := `
	INSERT INTO sessions (
		id, name, created_at, last_access_at, status, network, horizon_url, tx_hash,
		envelope_xdr, result_xdr, result_meta_xdr, pinned_endpoint,
		audit_hash, audit_signature, previous_session_hash,
		sim_request_json, sim_response_json, env_fingerprint, GLASSBOX_version, schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name = excluded.name,
		last_access_at = excluded.last_access_at,
		status = excluded.status,
		network = excluded.network,
		horizon_url = excluded.horizon_url,
		tx_hash = excluded.tx_hash,
		envelope_xdr = excluded.envelope_xdr,
		result_xdr = excluded.result_xdr,
		result_meta_xdr = excluded.result_meta_xdr,
		pinned_endpoint = excluded.pinned_endpoint,
		audit_hash = excluded.audit_hash,
		audit_signature = excluded.audit_signature,
		previous_session_hash = excluded.previous_session_hash,
		sim_request_json = excluded.sim_request_json,
		sim_response_json = excluded.sim_response_json,
		env_fingerprint = excluded.env_fingerprint,
		provenance_json = excluded.provenance_json,
		annotations_json = excluded.annotations_json,
		GLASSBOX_version = excluded.GLASSBOX_version,
		schema_version = excluded.schema_version
	`

	_, err := s.db.ExecContext(ctx, query,
		data.ID, data.Name, data.CreatedAt, data.LastAccessAt, data.Status,
		data.Network, data.HorizonURL, data.TxHash,
		data.EnvelopeXdr, data.ResultXdr, data.ResultMetaXdr, data.PinnedEndpoint,
		data.AuditHash, data.AuditSignature, data.PreviousSessionHash,
		data.SimRequestJSON, data.SimResponseJSON, data.EnvFingerprint, data.ProvenanceJSON, data.AnnotationsJSON, data.ErstVersion, data.SchemaVersion,
	)

	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	logger.Logger.Debug("Session saved", "id", data.ID, "tx_hash", data.TxHash)
	return nil
}

// SavePreservingSchemaVersion persists a session without bumping schema_version
// to the current constant. It exists for tests that need to seed rows with
// specific schema versions.
func (s *Store) SavePreservingSchemaVersion(ctx context.Context, data *Data) error {
	if data.ID == "" {
		return fmt.Errorf("session ID is required")
	}

	now := time.Now()
	if data.CreatedAt.IsZero() {
		data.CreatedAt = now
	}
	data.LastAccessAt = now

	if data.EnvFingerprint == "" {
		data.EnvFingerprint = BuildEnvFingerprint()
	}

	query := `
	INSERT INTO sessions (
		id, name, created_at, last_access_at, status, network, horizon_url, tx_hash,
		envelope_xdr, result_xdr, result_meta_xdr, pinned_endpoint,
		sim_request_json, sim_response_json, env_fingerprint, provenance_json, GLASSBOX_version, schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name = excluded.name,
		last_access_at = excluded.last_access_at,
		status = excluded.status,
		network = excluded.network,
		horizon_url = excluded.horizon_url,
		tx_hash = excluded.tx_hash,
		envelope_xdr = excluded.envelope_xdr,
		result_xdr = excluded.result_xdr,
		result_meta_xdr = excluded.result_meta_xdr,
		pinned_endpoint = excluded.pinned_endpoint,
		sim_request_json = excluded.sim_request_json,
		sim_response_json = excluded.sim_response_json,
		env_fingerprint = excluded.env_fingerprint,
		provenance_json = excluded.provenance_json,
		GLASSBOX_version = excluded.GLASSBOX_version,
		schema_version = excluded.schema_version
	`

	_, err := s.db.ExecContext(ctx, query,
		data.ID, data.Name, data.CreatedAt, data.LastAccessAt, data.Status,
		data.Network, data.HorizonURL, data.TxHash,
		data.EnvelopeXdr, data.ResultXdr, data.ResultMetaXdr, data.PinnedEndpoint,
		data.SimRequestJSON, data.SimResponseJSON, data.EnvFingerprint, data.ProvenanceJSON, data.ErstVersion, data.SchemaVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}
	return nil
}

// Load retrieves a session by ID
func (s *Store) Load(ctx context.Context, sessionID string) (*Data, error) {
	query := `
	SELECT id, name, created_at, last_access_at, status, network, horizon_url, tx_hash,
	       envelope_xdr, result_xdr, result_meta_xdr, pinned_endpoint,
	       audit_hash, audit_signature, previous_session_hash,
	       sim_request_json, sim_response_json, env_fingerprint, provenance_json, annotations_json, GLASSBOX_version, schema_version
	FROM sessions
	WHERE id = ?
	`

	var data Data
	var createdAt, lastAccessAt string
	var envFP, auditHash, auditSignature, prevSessionHash, provenanceJSON, annotationsJSON sql.NullString
	err := s.db.QueryRowContext(ctx, query, sessionID).Scan(
		&data.ID, &data.Name, &createdAt, &lastAccessAt, &data.Status,
		&data.Network, &data.HorizonURL, &data.TxHash,
		&data.EnvelopeXdr, &data.ResultXdr, &data.ResultMetaXdr, &data.PinnedEndpoint,
		&auditHash, &auditSignature, &prevSessionHash,
		&data.SimRequestJSON, &data.SimResponseJSON, &envFP, &provenanceJSON, &annotationsJSON, &data.ErstVersion, &data.SchemaVersion,
	)
	if envFP.Valid {
		data.EnvFingerprint = envFP.String
	}
	if auditHash.Valid {
		data.AuditHash = auditHash.String
	}
	if auditSignature.Valid {
		data.AuditSignature = auditSignature.String
	}
	if prevSessionHash.Valid {
		data.PreviousSessionHash = prevSessionHash.String
	}
	if provenanceJSON.Valid {
		data.ProvenanceJSON = provenanceJSON.String
	}
	if annotationsJSON.Valid {
		data.AnnotationsJSON = annotationsJSON.String
	}

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf(
			"session not found: %s\n"+
				"  Fix: run 'glassbox session list' to see available sessions",
			sessionID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf(
			"failed to load session %q: %w\n"+
				"  Fix: the session database may be corrupt. Check ~/.Glassbox/sessions.db",
			sessionID, err,
		)
	}

	if data.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, fmt.Errorf("failed to parse created_at for session %q: %w", sessionID, err)
	}
	if data.LastAccessAt, err = time.Parse(time.RFC3339, lastAccessAt); err != nil {
		return nil, fmt.Errorf("failed to parse last_access_at for session %q: %w", sessionID, err)
	}

	if schemaErr := ValidateSchemaVersion(data.SchemaVersion, data.ID); schemaErr != nil {
		return nil, schemaErr
	}

	upgraded, upgradeErr := UpgradeSessionData(&data)
	if upgradeErr != nil {
		return nil, upgradeErr
	}

	// Update last_access_at on load
	data.LastAccessAt = time.Now()
	if upgraded {
		if saveErr := s.Save(ctx, &data); saveErr != nil {
			return nil, fmt.Errorf("failed to persist upgraded session %q: %w", sessionID, saveErr)
		}
	} else {
		updateQuery := `UPDATE sessions SET last_access_at = ? WHERE id = ?`
		if _, updateErr := s.db.ExecContext(ctx, updateQuery, data.LastAccessAt, sessionID); updateErr != nil {
			logger.Logger.Warn("Failed to update last_access_at", "error", updateErr)
		}
	}

	return &data, nil
}

// LoadByName retrieves a saved session snapshot by its user-facing bookmark name.
func (s *Store) LoadByName(ctx context.Context, name string) (*Data, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf(
			"session name is required\n" +
				"  Fix: provide the bookmark name used when saving with 'glassbox session save --name <name>'",
		)
	}
	query := `SELECT id FROM sessions WHERE name = ?`
	var id string
	if err := s.db.QueryRowContext(ctx, query, name).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf(
				"session name not found: %q\n"+
					"  Fix: run 'glassbox session list' to see saved session names",
				name,
			)
		}
		return nil, fmt.Errorf("failed to load session by name: %w", err)
	}
	return s.Load(ctx, id)
}

// List returns recent sessions, ordered by last_access_at descending
func (s *Store) List(ctx context.Context, limit int) ([]*Data, error) {
	if limit <= 0 {
		limit = 50
	}

	queryBase := `
	SELECT id, name, created_at, last_access_at, status, network, horizon_url, tx_hash,
	       envelope_xdr, result_xdr, result_meta_xdr, pinned_endpoint,
	       audit_hash, audit_signature, previous_session_hash,
	       sim_request_json, sim_response_json, env_fingerprint, provenance_json, annotations_json, GLASSBOX_version, schema_version
	FROM sessions
	ORDER BY last_access_at DESC
	`

	var rows *sql.Rows
	var err error
	if limit > 0 {
		query := queryBase + "LIMIT ?"
		rows, err = s.db.QueryContext(ctx, query, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, queryBase)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []*Data
	for rows.Next() {
		var data Data
		var createdAt, lastAccessAt string

		envFP := sql.NullString{}
		auditHash := sql.NullString{}
		auditSignature := sql.NullString{}
		previousSessionHash := sql.NullString{}
		scanErr := rows.Scan(
			&data.ID, &data.Name, &createdAt, &lastAccessAt, &data.Status,
			&data.Network, &data.HorizonURL, &data.TxHash,
			&data.EnvelopeXdr, &data.ResultXdr, &data.ResultMetaXdr, &data.PinnedEndpoint,
			&auditHash, &auditSignature, &previousSessionHash,
			&data.SimRequestJSON, &data.SimResponseJSON, &envFP, &data.ErstVersion, &data.SchemaVersion,
		)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan session: %w", scanErr)
		}
		data.AuditHash = auditHash.String
		data.AuditSignature = auditSignature.String
		data.PreviousSessionHash = previousSessionHash.String
		data.EnvFingerprint = envFP.String
		sessions = append(sessions, &data)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("error iterating sessions: %w", rowsErr)
	}

	return sessions, nil
}

// Delete removes a session by ID
func (s *Store) Delete(ctx context.Context, sessionID string) error {
	query := `DELETE FROM sessions WHERE id = ?`
	result, err := s.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	logger.Logger.Debug("Session deleted", "id", sessionID)
	return nil
}

// Cleanup removes expired sessions and enforces max session limit
func (s *Store) Cleanup(ctx context.Context, ttl time.Duration, maxSessions int) error {
	now := time.Now()
	cutoff := now.Add(-ttl)

	// Delete expired sessions
	deleteExpired := `DELETE FROM sessions WHERE last_access_at < ?`
	result, err := s.db.ExecContext(ctx, deleteExpired, cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete expired sessions: %w", err)
	}

	expiredCount, _ := result.RowsAffected()
	if expiredCount > 0 {
		logger.Logger.Debug("Cleaned up expired sessions", "count", expiredCount)
	}

	// Enforce max sessions limit
	if maxSessions > 0 {
		countQuery := `SELECT COUNT(*) FROM sessions`
		var count int
		if countErr := s.db.QueryRowContext(ctx, countQuery).Scan(&count); countErr != nil {
			return fmt.Errorf("failed to count sessions: %w", countErr)
		}

		if count > maxSessions {
			excess := count - maxSessions
			deleteOldest := `
				DELETE FROM sessions
				WHERE id IN (
					SELECT id FROM sessions
					ORDER BY last_access_at ASC
					LIMIT ?
				)
			`
			delResult, delErr := s.db.ExecContext(ctx, deleteOldest, excess)
			if delErr != nil {
				return fmt.Errorf("failed to delete oldest sessions: %w", delErr)
			}

			deletedCount, _ := delResult.RowsAffected()
			if deletedCount > 0 {
				logger.Logger.Debug("Cleaned up excess sessions", "count", deletedCount)
			}
		}
	}

	return nil
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

type sessionRowScanner interface {
	Scan(dest ...interface{}) error
}

func (s *Store) scanSessionRow(row sessionRowScanner) (*Data, error) {
	var data Data
	var createdAt, lastAccessAt string
	var pinnedEndpoint sql.NullString
	var envFP sql.NullString

	err := row.Scan(
		&data.ID, &data.Name, &createdAt, &lastAccessAt, &data.Status,
		&data.Network, &data.HorizonURL, &data.TxHash,
		&data.EnvelopeXdr, &data.ResultXdr, &data.ResultMetaXdr, &pinnedEndpoint,
		&data.SimRequestJSON, &data.SimResponseJSON, &envFP, &data.ErstVersion, &data.SchemaVersion,
	)
	if err != nil {
		return nil, err
	}
	if pinnedEndpoint.Valid {
		data.PinnedEndpoint = pinnedEndpoint.String
	}
	if envFP.Valid {
		data.EnvFingerprint = envFP.String
	}

	if data.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, fmt.Errorf("failed to parse created_at: %w", err)
	}
	if data.LastAccessAt, err = time.Parse(time.RFC3339, lastAccessAt); err != nil {
		return nil, fmt.Errorf("failed to parse last_access_at: %w", err)
	}

	return &data, nil
}

// GenerateID creates a new session ID from transaction hash and timestamp
func GenerateID(txHash string) string {
	if txHash != "" {
		// Use first 8 chars of hash + timestamp for readability
		shortHash := txHash
		if len(shortHash) > 8 {
			shortHash = shortHash[:8]
		}
		return fmt.Sprintf("%s-%d", shortHash, time.Now().Unix())
	}
	// Fallback to timestamp-based ID
	return fmt.Sprintf("session-%d", time.Now().Unix())
}

// ToSimulationRequest converts stored JSON back to SimulationRequest
func (s *Data) ToSimulationRequest() (*simulator.SimulationRequest, error) {
	if s.SimRequestJSON == "" {
		return nil, fmt.Errorf("no simulation request data stored")
	}

	var req simulator.SimulationRequest
	if err := json.Unmarshal([]byte(s.SimRequestJSON), &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal simulation request: %w", err)
	}

	return &req, nil
}

// ToSimulationResponse converts stored JSON back to SimulationResponse
func (s *Data) ToSimulationResponse() (*simulator.SimulationResponse, error) {
	if s.SimResponseJSON == "" {
		return nil, fmt.Errorf("no simulation response data stored")
	}

	var resp simulator.SimulationResponse
	if err := json.Unmarshal([]byte(s.SimResponseJSON), &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal simulation response: %w", err)
	}

	return &resp, nil
}

// ── Session integrity validation ──────────────────────────────────────────────

// IntegrityIssue describes a single problem found during session integrity
// validation. Each issue carries a human-readable description and an optional
// remediation hint so the CLI can surface actionable output.
type IntegrityIssue struct {
	// Field is the session field that is invalid or missing (e.g. "TxHash").
	Field string
	// Description explains what is wrong.
	Description string
	// Hint is an optional actionable suggestion for the user.
	Hint string
}

// IntegrityReport is the output of ValidateIntegrity.
type IntegrityReport struct {
	// SessionID is the ID of the session that was checked.
	SessionID string
	// OK is true when no issues were found.
	OK bool
	// Issues lists every validation problem found.
	Issues []IntegrityIssue
	// SchemaCompatible reports whether the stored schema version is compatible
	// with the current binary's SchemaVersion constant.
	SchemaCompatible bool
	// StoredSchemaVersion is the schema_version value stored in the session row.
	StoredSchemaVersion int
}

// ValidateIntegrity checks a loaded session Data record for consistency and
// completeness. It validates:
//
//   - Required fields are non-empty (ID, TxHash, Network, Status)
//   - Status is a known value (active, saved, resumed, recovered, expired)
//   - CreatedAt and LastAccessAt are non-zero and in valid temporal order
//   - SchemaVersion is compatible with the current SchemaVersion constant
//   - EnvelopeXdr is non-empty when SimRequestJSON is also non-empty
//   - AuditHash and PreviousSessionHash are valid SHA-256 hex strings when set
//   - AuditSignature is a valid hex-encoded Ed25519 signature when set
//   - Audit chain fields are internally consistent (hash/signature pairing,
//     predecessor link, and no self-referential chain link)
//
// The function never modifies the session; it is safe to call concurrently.
func ValidateIntegrity(data *Data) *IntegrityReport {
	if data == nil {
		return &IntegrityReport{
			OK: false,
			Issues: []IntegrityIssue{{
				Field:       "Session",
				Description: "session record is nil",
				Hint:        "Load the session from the store again, or create a fresh one with 'glassbox debug <tx-hash>'.",
			}},
		}
	}

	report := &IntegrityReport{
		SessionID:           data.ID,
		SchemaCompatible:    data.SchemaVersion <= SchemaVersion,
		StoredSchemaVersion: data.SchemaVersion,
	}

	// Required: ID
	if data.ID == "" {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "ID",
			Description: "session ID is missing or empty",
			Hint:        "The session record is corrupt. Try starting a new debug session with 'glassbox debug <tx-hash>'.",
		})
	}

	// Required: TxHash
	if data.TxHash == "" {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "TxHash",
			Description: "transaction hash is missing or empty",
			Hint:        "The session was saved without a transaction hash. Re-run 'glassbox debug <tx-hash>' to create a valid session.",
		})
	} else if len(data.TxHash) != 64 {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "TxHash",
			Description: fmt.Sprintf("transaction hash %q has invalid length (%d, expected 64)", data.TxHash, len(data.TxHash)),
			Hint:        "A Stellar transaction hash must be 64 hexadecimal characters.",
		})
	}

	// Required: Network
	if data.Network == "" {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "Network",
			Description: "network is missing or empty",
			Hint:        "The session is missing its network field. Specify a network using --network (e.g., testnet, mainnet).",
		})
	} else {
		validNetworks := map[string]bool{
			"testnet": true, "mainnet": true, "futurenet": true,
		}
		if !validNetworks[data.Network] {
			report.Issues = append(report.Issues, IntegrityIssue{
				Field:       "Network",
				Description: fmt.Sprintf("network %q is not a recognized Stellar network", data.Network),
				Hint:        "Accepted values are: testnet, mainnet, futurenet.",
			})
		}
	}

	// Required: Status
	if data.Status == "" {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "Status",
			Description: "session status is missing or empty",
			Hint:        "The session record is missing a status. It may have been created by an incompatible version of Glassbox.",
		})
	} else {
		validStatuses := map[string]bool{
			"active": true, "saved": true, "resumed": true,
			"recovered": true, "expired": true,
		}
		if !validStatuses[data.Status] {
			report.Issues = append(report.Issues, IntegrityIssue{
				Field:       "Status",
				Description: fmt.Sprintf("unknown status value: %q", data.Status),
				Hint:        "Valid status values are: active, saved, resumed, recovered, expired.",
			})
		}
	}

	// Audit Chain Integrity: AuditHash format
	if data.AuditHash != "" {
		if err := validateSHA256HexHash("audit_hash", data.AuditHash); err != nil {
			report.Issues = append(report.Issues, IntegrityIssue{
				Field:       "AuditHash",
				Description: fmt.Sprintf("audit_hash is malformed: %v", err),
				Hint:        "The audit hash must be a 64-character hexadecimal SHA-256 string.",
			})
		}
	}

	// Audit Chain Integrity: PreviousSessionHash format
	if data.PreviousSessionHash != "" {
		if err := validateSHA256HexHash("previous_session_hash", data.PreviousSessionHash); err != nil {
			report.Issues = append(report.Issues, IntegrityIssue{
				Field:       "PreviousSessionHash",
				Description: fmt.Sprintf("previous_session_hash is malformed: %v", err),
				Hint:        "The predecessor session hash must be a 64-character hexadecimal SHA-256 string.",
			})
		}
	}

	// Audit Chain Integrity: AuditSignature format and pairing requirements
	if data.AuditSignature != "" {
		if err := validateEd25519SignatureHex("audit_signature", data.AuditSignature); err != nil {
			report.Issues = append(report.Issues, IntegrityIssue{
				Field:       "AuditSignature",
				Description: fmt.Sprintf("audit_signature is malformed: %v", err),
				Hint:        "The audit signature must be a 128-character hexadecimal Ed25519 signature.",
			})
		}
		if data.AuditHash == "" {
			report.Issues = append(report.Issues, IntegrityIssue{
				Field:       "AuditHash",
				Description: "audit_signature is present but audit_hash is missing",
				Hint:        "A signature can only exist if there is a hash to sign. Re-sign the session to fix this.",
			})
		}
	}
	if data.AuditHash != "" && data.AuditSignature == "" {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "AuditSignature",
			Description: "audit_hash is present but audit_signature is missing",
			Hint:        "Persisted audit-chain state must sign audit_hash. Re-sign the session before saving it.",
		})
	}
	if data.PreviousSessionHash != "" && data.AuditHash == "" {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "AuditHash",
			Description: "previous_session_hash is present but audit_hash is missing",
			Hint:        "A chained session needs its own audit_hash to anchor the predecessor link. Populate audit_hash or clear previous_session_hash for a genesis entry.",
		})
	}
	if data.PreviousSessionHash != "" && data.AuditSignature == "" {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "AuditSignature",
			Description: "previous_session_hash is present but audit_signature is missing",
			Hint:        "A chained session must sign its audit_hash. Populate audit_signature or clear previous_session_hash for a genesis entry.",
		})
	}
	if data.AuditHash != "" && data.PreviousSessionHash != "" && strings.EqualFold(data.AuditHash, data.PreviousSessionHash) {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "PreviousSessionHash",
			Description: "previous_session_hash points to the current session's own audit_hash (self-referential chain link)",
			Hint:        "Set previous_session_hash to the predecessor session's audit_hash, or leave it empty for the genesis entry.",
		})
	}

	// Timestamps: CreatedAt must be non-zero
	if data.CreatedAt.IsZero() {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "CreatedAt",
			Description: "created_at timestamp is missing or zero",
			Hint:        "The session creation time is missing. This record is likely corrupt; delete it and start over.",
		})
	}

	// Timestamps: LastAccessAt must be non-zero
	if data.LastAccessAt.IsZero() {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "LastAccessAt",
			Description: "last_access_at timestamp is missing or zero",
			Hint:        "The last-access timestamp is missing. Loading and re-saving the session should fix this.",
		})
	}

	// Timestamps: LastAccessAt must not precede CreatedAt
	if !data.CreatedAt.IsZero() && !data.LastAccessAt.IsZero() {
		if data.LastAccessAt.Before(data.CreatedAt) {
			report.Issues = append(report.Issues, IntegrityIssue{
				Field:       "LastAccessAt",
				Description: "last_access_at precedes created_at (temporal inconsistency)",
				Hint:        "The session timestamps are out of order. Re-saving the session will reset the last-access time to now.",
			})
		}
	}

	// Schema version forward compatibility
	if data.SchemaVersion > SchemaVersion {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field: "SchemaVersion",
			Description: fmt.Sprintf(
				"session schema version %d is newer than the supported version %d",
				data.SchemaVersion, SchemaVersion,
			),
			Hint: "Upgrade Glassbox to a newer release to open sessions created by a more recent version.",
		})
	} else if data.SchemaVersion < MinSupportedSchemaVersion {
		report.SchemaCompatible = false
		report.Issues = append(report.Issues, IntegrityIssue{
			Field: "SchemaVersion",
			Description: fmt.Sprintf(
				"session schema version %d is too old to load (minimum supported: %d, current: %d)",
				data.SchemaVersion, MinSupportedSchemaVersion, SchemaVersion,
			),
			Hint: "Re-run 'glassbox debug <tx-hash>' to recreate the session with the current format.",
		})
	}

	// Optional: Name length limit
	if len(data.Name) > 128 {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "Name",
			Description: fmt.Sprintf("session name is too long (%d characters, max 128)", len(data.Name)),
			Hint:        "Shorten the bookmark name and re-save with 'glassbox session save --name <shorter-name>'.",
		})
	}

	// Consistency: SimRequestJSON implies EnvelopeXdr
	if data.SimRequestJSON != "" && data.EnvelopeXdr == "" {
		report.Issues = append(report.Issues, IntegrityIssue{
			Field:       "EnvelopeXdr",
			Description: "simulation request is present but envelope XDR is missing",
			Hint:        "The session state is incomplete. Re-run 'glassbox debug <tx-hash>' to capture the full state.",
		})
	}

	report.OK = len(report.Issues) == 0
	return report
}

// validateSHA256HexHash verifies that a string is a 64-character hex-encoded SHA-256 hash.
func validateSHA256HexHash(field, hash string) error {
	if len(hash) != 64 {
		return fmt.Errorf("%s must be 64 hex characters, got %d", field, len(hash))
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return fmt.Errorf("%s is not valid hex: %w", field, err)
	}
	return nil
}

// validateEd25519SignatureHex verifies that sig is a 128-character hex-encoded
// Ed25519 signature (64 raw bytes).
func validateEd25519SignatureHex(field, sig string) error {
	if len(sig) != ed25519.SignatureSize*2 {
		return fmt.Errorf("%s must be %d hex characters, got %d", field, ed25519.SignatureSize*2, len(sig))
	}
	if _, err := hex.DecodeString(sig); err != nil {
		return fmt.Errorf("%s is not valid hex: %w", field, err)
	}
	return nil
}

// ── Store-level diagnostics ───────────────────────────────────────────────────

// StoreDiagnosticsResult summarises the health of all persisted sessions.
type StoreDiagnosticsResult struct {
	// TotalSessions is the number of sessions examined.
	TotalSessions int
	// HealthySessions is the count with no integrity issues.
	HealthySessions int
	// DegradedSessions is the count with at least one integrity issue.
	DegradedSessions int
	// Reports contains the IntegrityReport for every degraded session.
	Reports []*IntegrityReport
}

// RunStoreDiagnostics loads every session in the store and runs ValidateIntegrity
// on each one. It is safe to call from a background goroutine.
// If the store cannot be listed, an error is returned immediately.
func (s *Store) RunStoreDiagnostics(ctx context.Context) (*StoreDiagnosticsResult, error) {
	sessions, err := s.List(ctx, 0) // 0 → use default limit
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions for diagnostics: %w\n"+
			"  Run 'glassbox session list' to verify the session database is accessible.", err)
	}

	result := &StoreDiagnosticsResult{
		TotalSessions: len(sessions),
	}

	for _, data := range sessions {
		report := ValidateIntegrity(data)
		if report.OK {
			result.HealthySessions++
		} else {
			result.DegradedSessions++
			result.Reports = append(result.Reports, report)
		}
	}

	return result, nil
}

// Summary returns a human-readable one-liner for the diagnostics result.
func (r *StoreDiagnosticsResult) Summary() string {
	if r.TotalSessions == 0 {
		return "Session store: no sessions found."
	}
	if r.DegradedSessions == 0 {
		return fmt.Sprintf("Session store: %d session(s) — all healthy.", r.TotalSessions)
	}
	return fmt.Sprintf(
		"Session store: %d session(s), %d healthy, %d degraded — run 'glassbox session list' for details.",
		r.TotalSessions, r.HealthySessions, r.DegradedSessions,
	)
}
