package protocol

import (
	"crypto/rand"
	"encoding/hex"
)

// GenSessionID returns a new "<prefix>_<12 hex chars>" session identifier.
// Used for quote (qs_), chart (cs_), replay (rs_), and ad-hoc request IDs.
func GenSessionID(prefix string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read is documented to never fail on supported platforms;
		// fall back to a deterministic suffix so callers never see an empty id.
		return prefix + "_000000000000"
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
