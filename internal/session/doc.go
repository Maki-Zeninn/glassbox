// Package session provides persistent session management for Glassbox.
//
// A session represents a single debugging session - a complete trace of a
// transaction replayed locally against a WASM contract. Sessions capture the
// input request, WASM execution details, and the complete event stream from
// both local and on-chain runs for side-by-side comparison.
//
// Session State
//
// Sessions are stored as SQLite databases (glassbox.db) containing a single
// 'sessions' table. Each row represents one session with fields for metadata
// (name, network, transaction hash), execution state (status, timestamps),
// and audit integrity (hash, signature). Sessions support state transitions:
// active (in-progress), saved (archived), resumed (restored), recovered (from
// corruption), and expired (after TTL).
//
// The session package also manages derived artifacts:
//   - Checkpoints: Immutable snapshots for incremental replay and regression
//     testing
//   - Profiles: User preferences and UI state per session
//   - Manifests: Revision history and provenance tracking
//   - Archives: Compressed session bundles with checksums
//   - GC: Automatic cleanup of expired sessions
//
// Notes and Annotations
//
// Glassbox supports analyst collaboration features stored alongside session
// data but excluded from the cryptographic audit chain:
//
//   - Comments: Reviewer feedback attached to specific events
//   - Bookmarks: Named positions in the event stream for quick navigation
//   - Notes: Analyst working memory (code snippets, hypotheses, findings)
//
// These annotations live in the session's AnnotationsJSON field and share the
// same atomic write path as the session file. Crucially, annotations do NOT
// participate in the AuditHash computation - they are personal working memory
// that must not alter cryptographic evidence of the transaction execution.
//
// The canonical split:
//   - AuditHash covers session.json (which excludes AnnotationsJSON)
//   - AnnotationsJSON holds collaboration state: comments + bookmarks + notes
//
// Programming Model
//
// The primary entry points are Store and Data:
//
//   - Store: Manages the SQLite database, providing CRUD operations for
//     sessions and checkpoint management
//   - Data: The domain model representing a complete debug session
//
// Most callers should use the store methods (Open, Save, Get, List, Delete)
// rather than interacting with the underlying database directly.
//
// Thread Safety
//
// The Store type is safe for concurrent use. Individual Data objects are not
// thread-safe and should be protected by the caller when modified.
//
// Versioning
//
// Session schema versions are managed via SchemaVersion (database schema) and
// the JSON schema_version field in each session. Migration steps are defined
// in schema.go and executed automatically on open when necessary.
//
// Security Considerations
//
// - Session files are stored with restrictive permissions (0600)
// - AuditHash and AuditSignature provide cryptographic provenance
// - Encrypted sessions use envelope encryption with session keys
// - GC automatically removes expired sessions per retention policies
package session
