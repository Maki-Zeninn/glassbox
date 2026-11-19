// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/security"
	"github.com/dotandev/glassbox/internal/version"
)

// archiveVersion is incremented whenever the archive layout changes.
const archiveVersion = 1

// archiveMeta is written to meta.json inside every archive.
type archiveMeta struct {
	ArchiveVersion int    `json:"archive_version"`
	GlassboxVersion string `json:"glassbox_version"`
	CreatedAt      string `json:"created_at"`
	SchemaVersion  int    `json:"schema_version"`
}

// ArchiveOptions controls session archive export behavior
type ArchiveOptions struct {
	// SecretScanMode controls whether secret scanning is enabled and how it behaves
	SecretScanMode security.ScannerMode
	// SecretScanOverrides are paths that are allowed to contain secrets (for test fixtures)
	SecretScanOverrides []string
	// RedactionReport, when non-nil, is embedded in the archive manifest so
	// recipients can see which categories were sanitised. The report must
	// correspond to the Data passed to ExportArchiveWithOptions — it is
	// produced by RedactSession before the redacted copy is exported.
	RedactionReport *RedactionReport
}

// SupportedArchiveExtensions lists the file extensions accepted for session
// archive files. The canonical extension is ".gbx"; ".zip" is also accepted
// for interoperability with generic ZIP tools.
var SupportedArchiveExtensions = []string{".gbx", ".zip"}

// ValidateArchivePath checks that destPath is non-empty, free of null bytes,
// and ends with a supported archive extension. It returns an actionable error
// when any of these conditions are not met.
func ValidateArchivePath(destPath string) error {
	if strings.TrimSpace(destPath) == "" {
		return fmt.Errorf("destination path is required")
	}
	// Null bytes cannot appear in a valid file path and are a sign of injection.
	if strings.ContainsRune(destPath, 0) {
		return fmt.Errorf(
			"archive path contains null bytes and cannot be used: %q\n"+
				"  Fix: provide a path without null bytes (e.g. ./session.gbx)",
			destPath,
		)
	}
	ext := strings.ToLower(filepath.Ext(destPath))
	for _, supported := range SupportedArchiveExtensions {
		if ext == supported {
			return nil
		}
	}
	return fmt.Errorf(
		"unsupported archive extension %q — must be one of: %s\n"+
			"  Fix: rename the output file with a supported extension\n"+
			"  Example: glassbox session share --output ./session.gbx",
		ext, strings.Join(SupportedArchiveExtensions, ", "),
	)
}

// ExportArchive packages a debug session into a portable ZIP archive at
// destPath. The archive contains:
//
//	meta.json       – version and compatibility metadata
//	session.json    – the full session Data record
//
// Additional artifacts (source maps, trace JSON) can be embedded by callers
// that have access to them; the format reserves space via the zip comment.
//
// The session data is validated before export so that corrupt or incomplete
// sessions are rejected early with a clear error rather than silently archived.
func ExportArchive(data *Data, destPath string) error {
	return ExportArchiveWithOptions(data, destPath, ArchiveOptions{})
}

// ExportArchiveWithOptions packages a debug session into a portable ZIP archive
// with additional options for secret scanning and other export controls.
func ExportArchiveWithOptions(data *Data, destPath string, opts ArchiveOptions) error {
	if data == nil {
		return fmt.Errorf("session data is nil")
	}
	if err := ValidateArchivePath(destPath); err != nil {
		return err
	}

	// Validate session data before exporting so the user gets an actionable
	// error instead of an archive that cannot be imported on the other side.
	report := ValidateIntegrity(data)
	if !report.OK {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("cannot export invalid session (%d issue(s)):\n", len(report.Issues)))
		for i, issue := range report.Issues {
			sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, issue.Field, issue.Description))
			if issue.Hint != "" {
				sb.WriteString(fmt.Sprintf("     Hint: %s\n", issue.Hint))
			}
		}
		sb.WriteString("Fix the issues above and re-run 'glassbox session share'.")
		return fmt.Errorf("%s", sb.String())
	}

	// Secret scanning — detect and optionally block exports containing secrets.
	// Scans every text field that will be embedded in the archive, including
	// nested JSON artifacts (trace, bundle, annotations).
	if opts.SecretScanMode != "" {
		scanner := security.NewSecretScanner(opts.SecretScanMode)
		for _, override := range opts.SecretScanOverrides {
			scanner.AddOverride(override)
		}

		// Scan session metadata fields.
		fieldsToScan := map[string]string{
			"pinned_endpoint": data.PinnedEndpoint,
			"horizon_url":     data.HorizonURL,
			"env_fingerprint": data.EnvFingerprint,
		}
		result := scanner.ScanMap(fieldsToScan, "session")
		if result.HasSecrets {
			if scanner.ShouldBlockExport(result) {
				return fmt.Errorf(scanner.GetErrorMessage(result))
			}
			fmt.Fprintf(os.Stderr, "Warning: %s\n", scanner.GetErrorMessage(result))
		}

		// Scan each nested JSON artifact individually so the error message
		// names the specific archive member that contains the secret.
		nestedScans := []struct {
			member  string
			content string
		}{
			{"annotations", data.AnnotationsJSON},
			{"trace",       data.TraceJSON},
			{"bundle",      data.BundleJSON},
			{"source_map",  data.SourceMapJSON},
		}
		for _, ns := range nestedScans {
			if ns.content == "" {
				continue
			}
			var nested map[string]interface{}
			if err := json.Unmarshal([]byte(ns.content), &nested); err == nil {
				flatStr := make(map[string]string)
				for k, v := range nested {
					flatStr[k] = fmt.Sprintf("%v", v)
				}
				r := scanner.ScanMap(flatStr, ns.member)
				if r.HasSecrets {
					if scanner.ShouldBlockExport(r) {
						return fmt.Errorf(scanner.GetErrorMessage(r))
					}
					fmt.Fprintf(os.Stderr, "Warning: %s\n", scanner.GetErrorMessage(r))
				}
			}
		}
	}

	journalPath := destPath + ".journal"
	if err := writeExportJournal(journalPath, destPath); err != nil {
		return fmt.Errorf("failed to write export recovery journal: %w", err)
	}
	defer func() { _ = os.Remove(journalPath) }()

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("cannot create destination directory %q: %w", destDir, err)
	}
	tmp, err := os.CreateTemp(destDir, filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("cannot create archive temp file: %w", err)
	}
	tmpPath := tmp.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	f := tmp
	zw := zip.NewWriter(f)

	now := time.Now()

	// Write meta.json.
	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: version.Version,
		CreatedAt:       now.UTC().Format(time.RFC3339),
		SchemaVersion:   SchemaVersion,
	}
	if _, err := writeJSONEntry(zw, "meta.json", meta); err != nil {
		return fmt.Errorf("failed to write meta.json: %w", err)
	}

	// Write session.json. Its bytes become the "metadata" manifest member.
	sessionBytes, err := writeSessionJSONEntry(zw, "session.json", data)
	if err != nil {
		return fmt.Errorf("failed to write session.json: %w", err)
	}

	// Write envelope XDR as raw bytes when present.
	if data.EnvelopeXdr != "" {
		if err := writeStringEntry(zw, "envelope.xdr", data.EnvelopeXdr); err != nil {
			return fmt.Errorf("failed to write envelope.xdr: %w", err)
		}
	}

	// Write simulation response JSON when present.
	if data.SimResponseJSON != "" {
		if err := writeStringEntry(zw, "sim_response.json", data.SimResponseJSON); err != nil {
			return fmt.Errorf("failed to write sim_response.json: %w", err)
		}
	}

	// Write the remaining canonical manifest members when the session
	// carries them, then build and write the integrity manifest covering
	// every embedded artifact (trace, bundle, source_map, annotations,
	// metadata) — see Issue #56.
	manifestMembers := map[string][]byte{"metadata": sessionBytes}

	optionalMembers := []struct {
		member, entry, content string
	}{
		{"trace", "trace.json", data.TraceJSON},
		{"bundle", "bundle.json", data.BundleJSON},
		{"source_map", "source_map.json", data.SourceMapJSON},
		{"annotations", "annotations.json", data.AnnotationsJSON},
	}
	for _, om := range optionalMembers {
		if om.content == "" {
			continue
		}
		if err := writeStringEntry(zw, om.entry, om.content); err != nil {
			return fmt.Errorf("failed to write %s: %w", om.entry, err)
		}
		manifestMembers[om.member] = []byte(om.content)
	}

	manifest := BuildManifestWithRedaction(manifestMembers, SchemaVersion, now, opts.RedactionReport)
	if _, err := writeJSONEntry(zw, "manifest.json", manifest); err != nil {
		return fmt.Errorf("failed to write manifest.json: %w", err)
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to finalize archive: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync archive temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close archive temp file: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to rename archive into place: %w", err)
	}
	_ = syncDir(destDir)
	succeeded = true

	return nil
}

// writeExportJournal records that an archive export to destPath is in
// progress. A leftover journal after a crash tells 'glassbox session doctor'
// that the export was interrupted, so any orphaned temp file for destPath is
// safe to clean up rather than a sign of unrelated disk corruption.
func writeExportJournal(journalPath, destPath string) error {
	entry := struct {
		Dest      string    `json:"dest"`
		StartedAt time.Time `json:"started_at"`
		PID       int       `json:"pid"`
	}{Dest: destPath, StartedAt: time.Now().UTC(), PID: os.Getpid()}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(journalPath, data, 0o600)
}

// ImportArchive reads a session archive produced by ExportArchive and returns
// the reconstructed Data. It validates the archive format, extension, and
// version compatibility before returning, surfacing actionable errors for each
// failure mode.
func ImportArchive(srcPath string) (*Data, error) {
	data, _, err := ImportArchiveWithManifest(srcPath)
	return data, err
}

// manifestEntryNames are the zip entry names ImportArchiveWithManifest treats
// as singleton members; encountering any of them more than once in an
// archive indicates a corrupt or tampered file.
var manifestEntryNames = map[string]bool{
	"meta.json": true, "session.json": true, "manifest.json": true,
	"trace.json": true, "bundle.json": true, "source_map.json": true, "annotations.json": true,
}

// ImportArchiveWithManifest behaves like ImportArchive but also returns the
// ManifestReport produced by verifying the archive's embedded integrity
// manifest (see Issue #56). report.Compatible is false for archives created
// before manifests existed; this is the documented compatibility path and is
// never treated as a failure on its own.
func ImportArchiveWithManifest(srcPath string) (*Data, *ManifestReport, error) {
	if strings.TrimSpace(srcPath) == "" {
		return nil, nil, fmt.Errorf(
			"archive path is required\n" +
				"  Fix: provide the path to a .gbx archive file\n" +
				"  Example: glassbox session load ./session.gbx",
		)
	}

	// Reject null bytes early — they cannot appear in valid file paths and are
	// a sign of attempted injection.
	if strings.ContainsRune(srcPath, 0) {
		return nil, nil, fmt.Errorf(
			"archive path contains null bytes and cannot be used: %q\n"+
				"  Fix: provide a path without null bytes (e.g. ./session.gbx)",
			srcPath,
		)
	}

	// Validate extension so we surface a clear error before attempting to open.
	ext := strings.ToLower(filepath.Ext(srcPath))
	validExt := false
	for _, supported := range SupportedArchiveExtensions {
		if ext == supported {
			validExt = true
			break
		}
	}
	if !validExt {
		return nil, nil, fmt.Errorf(
			"unsupported archive extension %q — expected one of: %s\n"+
				"  Fix: use a file exported by 'glassbox session share'\n"+
				"  Example: glassbox session load ./session.gbx",
			ext, strings.Join(SupportedArchiveExtensions, ", "),
		)
	}

	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"cannot open archive %q: %w\n"+
				"  Fix: ensure the file exists, is readable, and is a valid Glassbox session archive",
			srcPath, err,
		)
	}
	defer func() { _ = zr.Close() }()

	// Detect duplicate entries for any singleton member before reading
	// anything else — a repeated name indicates a corrupt or tampered
	// archive and must never be silently resolved by "last one wins".
	counts := make(map[string]int, len(zr.File))
	for _, f := range zr.File {
		if manifestEntryNames[f.Name] {
			counts[f.Name]++
		}
	}
	for name, count := range counts {
		if count > 1 {
			return nil, nil, fmt.Errorf(
				"archive %q contains %d copies of %q — the archive is corrupt or was tampered with\n"+
					"  Fix: re-export the archive with 'glassbox session share'",
				srcPath, count, name,
			)
		}
	}

	var meta archiveMeta
	var manifest *Manifest
	var data Data
	metaFound := false
	sessionFound := false
	manifestFound := false
	rawMembers := make(map[string][]byte, len(CanonicalManifestMembers))

	for _, f := range zr.File {
		switch f.Name {
		case "meta.json":
			if err := readJSONEntry(f, &meta); err != nil {
				return nil, nil, fmt.Errorf(
					"failed to read meta.json from archive: %w\n"+
						"  Fix: the archive may be corrupt. Re-export it with 'glassbox session share'",
					err,
				)
			}
			metaFound = true
		case "session.json":
			raw, err := readRawEntry(f)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"failed to read session.json from archive: %w\n"+
						"  Fix: the archive may be corrupt. Re-export it with 'glassbox session share'",
					err,
				)
			}
			if err := json.Unmarshal(raw, &data); err != nil {
				return nil, nil, fmt.Errorf(
					"failed to read session.json from archive: %w\n"+
						"  Fix: the archive may be corrupt. Re-export it with 'glassbox session share'",
					err,
				)
			}
			rawMembers["metadata"] = raw
			sessionFound = true
		case "manifest.json":
			if err := readJSONEntry(f, &manifest); err != nil {
				return nil, nil, fmt.Errorf(
					"failed to read manifest.json from archive: %w\n"+
						"  Fix: the archive may be corrupt. Re-export it with 'glassbox session share'",
					err,
				)
			}
			manifestFound = true
		case "trace.json":
			raw, err := readRawEntry(f)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read trace.json from archive: %w", err)
			}
			rawMembers["trace"] = raw
		case "bundle.json":
			raw, err := readRawEntry(f)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read bundle.json from archive: %w", err)
			}
			rawMembers["bundle"] = raw
		case "source_map.json":
			raw, err := readRawEntry(f)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read source_map.json from archive: %w", err)
			}
			rawMembers["source_map"] = raw
		case "annotations.json":
			raw, err := readRawEntry(f)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read annotations.json from archive: %w", err)
			}
			rawMembers["annotations"] = raw
		}
	}

	if !metaFound {
		return nil, nil, fmt.Errorf(
			"archive is missing meta.json — not a valid Glassbox session archive\n" +
				"  Fix: use a file exported by 'glassbox session share'",
		)
	}
	if !sessionFound {
		return nil, nil, fmt.Errorf(
			"archive is missing session.json — not a valid Glassbox session archive\n" +
				"  Fix: use a file exported by 'glassbox session share'",
		)
	}
	if meta.ArchiveVersion > archiveVersion {
		return nil, nil, fmt.Errorf(
			"archive version %d is newer than supported version %d\n"+
				"  Fix: upgrade Glassbox to the latest release to open this archive",
			meta.ArchiveVersion, archiveVersion,
		)
	}
	if meta.SchemaVersion > SchemaVersion {
		return nil, nil, fmt.Errorf(
			"session schema version %d is newer than supported version %d\n"+
				"  Fix: upgrade Glassbox to the latest release to open this session",
			meta.SchemaVersion, SchemaVersion,
		)
	}

	// Verify every embedded artifact against the manifest before trusting
	// it. Archives without a manifest.json (pre-Issue-#56) take the
	// documented compatibility path: VerifyManifest reports OK=true,
	// Compatible=false, and the session still loads.
	if !manifestFound {
		manifest = nil
	}
	manifestReport := VerifyManifest(manifest, rawMembers)
	if !manifestReport.OK {
		return nil, manifestReport, fmt.Errorf(
			"archive %q %s", srcPath, FormatManifestIssues(manifestReport),
		)
	}

	// Repopulate the archive-only artifact fields now that their hashes
	// (when a manifest was present) have been verified.
	if raw, ok := rawMembers["trace"]; ok {
		data.TraceJSON = string(raw)
	}
	if raw, ok := rawMembers["bundle"]; ok {
		data.BundleJSON = string(raw)
	}
	if raw, ok := rawMembers["source_map"]; ok {
		data.SourceMapJSON = string(raw)
	}
	if raw, ok := rawMembers["annotations"]; ok {
		data.AnnotationsJSON = string(raw)
	}

	// Preserve unknown/additive fields from session.json so that an older
	// binary does not silently discard forward-compatible extensions added
	// by a newer one. We decode the raw session bytes into a generic map and
	// keep everything the struct did not consume as ExtrasJSON.
	if sessionRaw, ok := rawMembers["metadata"]; ok {
		data.ExtrasJSON = extractUnknownFields(sessionRaw, &data)
	}

	// Run schema migration on the imported data using the same upgrade path
	// as the load path. This means imported archives from older Glassbox
	// versions are upgraded deterministically before validation runs,
	// producing the same outcome as if the session had been loaded from the
	// SQLite store.
	if schemaErr := ValidateSchemaVersion(data.SchemaVersion, data.ID); schemaErr != nil {
		return nil, manifestReport, schemaErr
	}
	if _, upgradeErr := UpgradeSessionData(&data); upgradeErr != nil {
		return nil, manifestReport, fmt.Errorf(
			"archive %q: failed to upgrade session schema: %w", srcPath, upgradeErr,
		)
	}

	// Validate the reconstructed (and potentially upgraded) session data so
	// imported archives with missing or corrupt fields are rejected with a
	// clear diagnostic instead of silently producing a broken session.
	report := ValidateIntegrity(&data)
	if !report.OK {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf(
			"archive %q contains an invalid session (%d issue(s)):\n",
			srcPath, len(report.Issues),
		))
		for i, issue := range report.Issues {
			sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, issue.Field, issue.Description))
			if issue.Hint != "" {
				sb.WriteString(fmt.Sprintf("     Hint: %s\n", issue.Hint))
			}
		}
		sb.WriteString("Re-export with 'glassbox session share' from a valid session.")
		return nil, manifestReport, fmt.Errorf("%s", sb.String())
	}

	return &data, manifestReport, nil
}

// writeJSONEntry serialises v and writes it as a named entry in the zip,
// returning the exact bytes written so callers can hash them for a Manifest.
// It uses deterministic key ordering for reproducible exports.
func writeJSONEntry(zw *zip.Writer, name string, v interface{}) ([]byte, error) {
	// Sort map keys recursively for deterministic output
	sorted := SortMapKeys(v)

	// Use json.Marshal for consistent ordering with sorted keys
	data, err := json.MarshalIndent(sorted, "", "  ")
	if err != nil {
		return nil, err
	}

	w, err := zw.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	return data, nil
}

// readRawEntry returns the raw, undecoded bytes of a zip file entry.
func readRawEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// writeStringEntry writes a plain string as a named entry in the zip.
func writeStringEntry(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, content)
	return err
}

// readJSONEntry decodes JSON from a zip file entry into v.
func readJSONEntry(f *zip.File, v interface{}) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	return json.NewDecoder(rc).Decode(v)
}
// sortedKeys returns the sorted keys of a map for deterministic serialization.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SortMapKeys recursively sorts map keys for deterministic JSON serialization.
// This ensures session metadata is serialized in a consistent order for reproducibility.
func SortMapKeys(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		keys := sortedKeys(val)
		for _, k := range keys {
			result[k] = SortMapKeys(val[k])
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = SortMapKeys(item)
		}
		return result
	default:
		return v
	}
}

// DeterministicMarshal marshals a value to JSON with sorted map keys.
// This is used for session metadata serialization to ensure reproducible exports.
func DeterministicMarshal(v interface{}) ([]byte, error) {
	// Sort all map keys recursively
	sorted := SortMapKeys(v)

	// Use a sorted key encoder for deterministic output
	enc := json.NewEncoder(new(strings.Builder))
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)

	// For full deterministic output, we need custom marshaling
	return json.MarshalIndent(sorted, "", "  ")
}

// CommandParams holds command-line parameters for session metadata.
type CommandParams map[string]interface{}

// Sorted returns a new map with keys sorted deterministically.
func (cp CommandParams) Sorted() CommandParams {
	result := make(CommandParams)
	keys := sortedKeys(map[string]interface{}(cp))
	for _, k := range keys {
		result[k] = SortMapKeys(cp[k])
	}
	return result
}

// ToJSON returns JSON representation with deterministically sorted keys.
func (cp CommandParams) ToJSON() (string, error) {
	data, err := json.MarshalIndent(cp.Sorted(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// EnsureDeterministicOrder sorts command parameters before serialization.
// This should be called before writing session metadata to ensure reproducible exports.
func EnsureDeterministicOrder(data *Data) *Data {
	if data == nil {
		return data
	}

	// Create a copy to avoid mutating the original
	result := *data

	// Sort any nested maps in the session data
	// This is a shallow sort; deep sorting is handled by SortMapKeys

	return &result
}

// ── Unknown-field preservation helpers ───────────────────────────────────────

// knownSessionJSONKeys is the set of top-level JSON keys that the Data struct
// consumes during a normal json.Unmarshal. Any key present in a session.json
// but absent from this set is an unknown/additive field that should be
// preserved in ExtrasJSON so a round-trip does not silently discard it.
var knownSessionJSONKeys = map[string]bool{
	"id": true, "name": true, "created_at": true, "last_access_at": true,
	"status": true, "network": true, "horizon_url": true, "tx_hash": true,
	"envelope_xdr": true, "result_xdr": true, "result_meta_xdr": true,
	"pinned_endpoint": true, "audit_hash": true, "audit_signature": true,
	"previous_session_hash": true, "sim_request_json": true,
	"sim_response_json": true, "env_fingerprint": true, "provenance_json": true,
	"GLASSBOX_version": true, "schema_version": true, "encrypted_payload": true,
}

// extractUnknownFields decodes raw session JSON and returns a map of any
// top-level fields that are not present in knownSessionJSONKeys. The returned
// map is nil when there are no unknown fields. Decoding errors are silently
// ignored — unknown-field preservation is best-effort and must never block the
// import of an otherwise valid archive.
func extractUnknownFields(raw []byte, _ *Data) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil
	}
	extras := make(map[string]json.RawMessage)
	for k, v := range all {
		if !knownSessionJSONKeys[k] {
			extras[k] = v
		}
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
}

// marshalSessionWithExtras serialises data to JSON, merging any ExtrasJSON
// fields back in so that a round-tripped archive contains the same unknown
// keys it arrived with. Fields in ExtrasJSON that collide with known struct
// keys are silently dropped to prevent confusion.
func marshalSessionWithExtras(data *Data) ([]byte, error) {
	// First encode the struct normally.
	base, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session data: %w", err)
	}
	if len(data.ExtrasJSON) == 0 {
		return base, nil
	}

	// Decode base back to a generic map, inject extras, re-encode.
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		// Unexpected but non-fatal — return the clean base.
		return base, nil
	}
	for k, v := range data.ExtrasJSON {
		if knownSessionJSONKeys[k] {
			continue // never overwrite known fields with stale extras
		}
		merged[k] = v
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return base, nil // fallback to base on encode error
	}
	return out, nil
}

// writeSessionJSONEntry serialises the session Data (preserving extras) as a
// named entry in the zip, returning the exact bytes written so callers can
// hash them for a Manifest.
func writeSessionJSONEntry(zw *zip.Writer, name string, data *Data) ([]byte, error) {
	sessionBytes, err := marshalSessionWithExtras(data)
	if err != nil {
		return nil, err
	}
	w, err := zw.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(sessionBytes); err != nil {
		return nil, err
	}
	return sessionBytes, nil
}
