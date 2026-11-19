// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package secutil

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/dotandev/glassbox/internal/logger"
)

// SecureBytes wraps a sensitive byte slice, attempting to pin it in RAM with
// mlock (Linux/macOS) or VirtualLock (Windows) so the OS cannot page the
// material to disk. Callers MUST call Zero() when finished; a finalizer is
// registered only as a last-resort safety net and is not reliable.
type SecureBytes struct {
	mu     sync.Mutex
	buf    []byte
	locked bool
	zeroed bool
}

// MlockUnavailable is set to true when NewSecureBytes could not lock memory.
// Signers and audit metadata readers inspect this to surface a WARNING that
// mlock was unavailable for a given signing session.
var (
	mlockMu          sync.Mutex
	MlockUnavailable bool
	MlockWarning     string
)

// ErrMlockUnavailable is returned (wrapped) when the platform lock call fails.
// NewSecureBytes still succeeds in that case; callers that care about hard
// failures for other reasons should check errors.Is against platform-specific
// allocation errors instead.
var ErrMlockUnavailable = fmt.Errorf("mlock unavailable")

// NewSecureBytes allocates a size-byte buffer and attempts to lock it in memory.
// If locking fails (sandbox, permissions, unsupported platform), the bytes are
// still usable and a WARNING is logged; MlockUnavailable is set so audit
// metadata can record the condition. Allocation failures (size < 0) return an
// error that callers must treat as fatal.
func NewSecureBytes(size int) (*SecureBytes, error) {
	if size < 0 {
		return nil, fmt.Errorf("secure bytes: invalid size %d", size)
	}
	buf := make([]byte, size)
	sb := &SecureBytes{buf: buf}

	if err := platformLock(buf); err != nil {
		mlockMu.Lock()
		MlockUnavailable = true
		MlockWarning = fmt.Sprintf("WARNING: memory lock unavailable (%v); signing key may be paged to disk", err)
		mlockMu.Unlock()
		if logger.Logger != nil {
			logger.Logger.Warn("mlock unavailable for SecureBytes; key material may be swapped to disk",
				"error", err.Error(),
				"size", size,
			)
		}
		// Soft failure — keep the buffer unlocked rather than rejecting the key.
	} else {
		sb.locked = true
	}

	runtime.SetFinalizer(sb, func(s *SecureBytes) {
		// Finalizer is a last-resort safety net; callers must call Zero().
		_ = s.Zero()
	})
	return sb, nil
}

// Bytes returns the underlying slice directly (no copy). Callers must not
// retain the slice past Zero().
func (s *SecureBytes) Bytes() []byte {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.zeroed {
		return nil
	}
	return s.buf
}

// CopyFrom copies src into the secure buffer. src longer than the buffer is
// truncated; shorter src leaves the tail unchanged.
func (s *SecureBytes) CopyFrom(src []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.zeroed || s.buf == nil {
		return
	}
	n := copy(s.buf, src)
	_ = n
}

// Locked reports whether the backing memory is currently mlock'd / VirtualLock'd.
func (s *SecureBytes) Locked() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked && !s.zeroed
}

// Zero overwrites the buffer with zeros, unlocks the pages, and clears the
// finalizer. It is safe to call multiple times.
func (s *SecureBytes) Zero() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.zeroed {
		return nil
	}
	for i := range s.buf {
		s.buf[i] = 0
	}
	runtime.KeepAlive(s.buf)
	if s.locked {
		_ = platformUnlock(s.buf)
		s.locked = false
	}
	s.zeroed = true
	runtime.SetFinalizer(s, nil)
	return nil
}

// MlockStatus returns a short status string suitable for audit log metadata.
func MlockStatus() string {
	mlockMu.Lock()
	defer mlockMu.Unlock()
	if MlockUnavailable {
		if MlockWarning != "" {
			return MlockWarning
		}
		return "WARNING: mlock unavailable"
	}
	return "mlock active"
}
