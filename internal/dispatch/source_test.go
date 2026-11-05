package dispatch

import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSourceManifestRejectsIncompleteOrAmbiguousIdentity(t *testing.T) {
	var id pgtype.UUID
	require.NoError(t, id.Scan("00000000-0000-4000-8000-000000000001"))
	valid := SourceManifest{ID: id, ClusterID: "cluster", Topic: "send", Incarnation: "reviewed", PartitionCount: 2}
	require.NoError(t, valid.Validate())
	for _, change := range []func(*SourceManifest){
		func(s *SourceManifest) { s.ID = pgtype.UUID{} }, func(s *SourceManifest) { s.ClusterID = "" }, func(s *SourceManifest) { s.Topic = "../send" }, func(s *SourceManifest) { s.Incarnation = "wrong\nreference" }, func(s *SourceManifest) { s.PartitionCount = 0 },
	} {
		candidate := valid
		change(&candidate)
		require.ErrorIs(t, candidate.Validate(), ErrQuarantineSource)
	}
	_, err := NewSourceOutbox(nil, nil, valid)
	require.Error(t, err)
}
