package dispatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

var ErrReplayInput = errors.New("invalid replay request")

// ReplayPosition selects immutable quarantine evidence, not a campaign ID.
// Topic names alone cannot identify a physical source incarnation.
type ReplayPosition struct {
	LogSourceID pgtype.UUID
	Partition   int32
	Offset      int64
}

// ReplayRequest selects one bounded, explicitly scoped operator action.
// Its fields are untrusted input; a fingerprint does not confer authority.
type ReplayRequest struct {
	ApplicationID   pgtype.UUID
	ID              pgtype.UUID
	Positions       []ReplayPosition
	ChangeReference string
}

func validReplayUUID(id pgtype.UUID) bool {
	return id.Valid && id.Bytes != [16]byte{}
}

func validReplayText(value string) bool {
	return len(value) > 0 && len(value) <= 255 && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

// Validate checks the complete selection without changing its order or granting
// authority. CLI adapters use it before opening database or publisher resources.
func (r ReplayRequest) Validate() error {
	_, err := r.normalized()
	return err
}

func (r ReplayRequest) normalized() (ReplayRequest, error) {
	if !validReplayUUID(r.ApplicationID) || !validReplayUUID(r.ID) || len(r.Positions) < 1 || len(r.Positions) > 100 ||
		!validReplayText(r.ChangeReference) {
		return ReplayRequest{}, ErrReplayInput
	}
	positions := slices.Clone(r.Positions)
	for _, p := range positions {
		if !validReplayUUID(p.LogSourceID) || p.Partition < 0 || p.Offset < 0 {
			return ReplayRequest{}, ErrReplayInput
		}
	}
	slices.SortFunc(positions, func(a, b ReplayPosition) int {
		if n := bytes.Compare(a.LogSourceID.Bytes[:], b.LogSourceID.Bytes[:]); n != 0 {
			return n
		}
		if a.Partition < b.Partition {
			return -1
		}
		if a.Partition > b.Partition {
			return 1
		}
		if a.Offset < b.Offset {
			return -1
		}
		if a.Offset > b.Offset {
			return 1
		}
		return 0
	})
	for i := 1; i < len(positions); i++ {
		if positions[i] == positions[i-1] {
			return ReplayRequest{}, ErrReplayInput
		}
	}
	r.Positions = positions
	return r, nil
}

// Fingerprint binds the complete normalized selection, reference and verified
// actor/grant to a stable request identity. Reordering positions is immaterial;
// changing scope, identity or any selected position changes the digest.
// Callers must separately verify the operational credential and current DB authority.
func (r ReplayRequest) Fingerprint(staffID int64, grantID pgtype.UUID) ([sha256.Size]byte, error) {
	var empty [sha256.Size]byte
	if staffID <= 0 || !validReplayUUID(grantID) {
		return empty, ErrReplayInput
	}
	r, err := r.normalized()
	if err != nil {
		return empty, err
	}
	data := []byte("hello:notification-replay-request:v1\x00")
	data = append(data, r.ApplicationID.Bytes[:]...)
	data = append(data, r.ID.Bytes[:]...)
	data = binary.BigEndian.AppendUint64(data, uint64(staffID))
	data = append(data, grantID.Bytes[:]...)
	data = binary.BigEndian.AppendUint16(data, uint16(len(r.ChangeReference)))
	data = append(data, r.ChangeReference...)
	data = binary.BigEndian.AppendUint16(data, uint16(len(r.Positions)))
	for _, p := range r.Positions {
		data = append(data, p.LogSourceID.Bytes[:]...)
		data = binary.BigEndian.AppendUint32(data, uint32(p.Partition))
		data = binary.BigEndian.AppendUint64(data, uint64(p.Offset))
	}
	return sha256.Sum256(data), nil
}
