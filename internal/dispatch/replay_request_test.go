package dispatch

import (
	"encoding/hex"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"testing"
)

func replayTestUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, id.Scan(value))
	return id
}

func replayTestRequest(t *testing.T) ReplayRequest {
	return ReplayRequest{ApplicationID: replayTestUUID(t, "10000000-0000-0000-0000-000000000001"), ID: replayTestUUID(t, "20000000-0000-0000-0000-000000000001"), ChangeReference: "INC-123", Positions: []ReplayPosition{
		{LogSourceID: replayTestUUID(t, "30000000-0000-0000-0000-000000000001"), Partition: 1, Offset: 42},
		{LogSourceID: replayTestUUID(t, "30000000-0000-0000-0000-000000000001"), Partition: 0, Offset: 7},
	}}
}

func TestReplayRequestFingerprint(t *testing.T) {
	request := replayTestRequest(t)
	grant := replayTestUUID(t, "40000000-0000-0000-0000-000000000001")
	original := append([]ReplayPosition(nil), request.Positions...)
	first, err := request.Fingerprint(7, grant)
	require.NoError(t, err)
	require.Equal(t, "d777b492f9ba3c9c00768eac68e0bd5114dbeb3afc05cd256f811e6f9dd3cea4", hex.EncodeToString(first[:]), "persisted v1 fingerprint encoding must remain stable")
	require.Equal(t, original, request.Positions, "fingerprinting must not reorder caller input")
	request.Positions[0], request.Positions[1] = request.Positions[1], request.Positions[0]
	reordered, err := request.Fingerprint(7, grant)
	require.NoError(t, err)
	require.Equal(t, first, reordered, "selection order must not create a different request")
	require.EqualValues(t, 0, request.Positions[0].Partition, "fingerprinting must not mutate caller memory")
	for _, change := range []func(*ReplayRequest){
		func(r *ReplayRequest) { r.ChangeReference = "INC-456" },
		func(r *ReplayRequest) { r.Positions[0].Offset++ },
		func(r *ReplayRequest) { r.Positions = r.Positions[:1] },
		func(r *ReplayRequest) { r.ID = grant },
		func(r *ReplayRequest) { r.ApplicationID = grant },
	} {
		other := request
		other.Positions = append([]ReplayPosition(nil), request.Positions...)
		change(&other)
		hash, err := other.Fingerprint(7, grant)
		require.NoError(t, err)
		require.NotEqual(t, first, hash)
	}
	hash, err := request.Fingerprint(8, grant)
	require.NoError(t, err)
	require.NotEqual(t, first, hash)
	hash, err = request.Fingerprint(7, request.ID)
	require.NoError(t, err)
	require.NotEqual(t, first, hash)
}

func TestReplayRequestRejectsInvalidSelection(t *testing.T) {
	for name, change := range map[string]func(*ReplayRequest){
		"missing app":        func(r *ReplayRequest) { r.ApplicationID = pgtype.UUID{} },
		"zero app":           func(r *ReplayRequest) { r.ApplicationID = pgtype.UUID{Valid: true} },
		"missing request":    func(r *ReplayRequest) { r.ID = pgtype.UUID{} },
		"empty":              func(r *ReplayRequest) { r.Positions = nil },
		"too many":           func(r *ReplayRequest) { r.Positions = make([]ReplayPosition, 101) },
		"duplicate":          func(r *ReplayRequest) { r.Positions[1] = r.Positions[0] },
		"source":             func(r *ReplayRequest) { r.Positions[0].LogSourceID = pgtype.UUID{} },
		"negative partition": func(r *ReplayRequest) { r.Positions[0].Partition = -1 },
		"negative offset":    func(r *ReplayRequest) { r.Positions[0].Offset = -1 },
		"empty reference":    func(r *ReplayRequest) { r.ChangeReference = "" },
		"whitespace":         func(r *ReplayRequest) { r.ChangeReference = " INC-123" },
		"control":            func(r *ReplayRequest) { r.ChangeReference = "INC\n123" },
		"invalid utf8":       func(r *ReplayRequest) { r.ChangeReference = string([]byte{255}) },
	} {
		t.Run(name, func(t *testing.T) {
			request := replayTestRequest(t)
			change(&request)
			_, err := request.Fingerprint(7, request.ID)
			require.ErrorIs(t, err, ErrReplayInput)
		})
	}
	request := replayTestRequest(t)
	for _, actor := range []int64{0, -1} {
		_, err := request.Fingerprint(actor, request.ID)
		require.ErrorIs(t, err, ErrReplayInput)
	}
	_, err := request.Fingerprint(7, pgtype.UUID{})
	require.ErrorIs(t, err, ErrReplayInput)
}
