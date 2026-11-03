//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/database"
	"hello/internal/database/sqlc"
)

func TestScopedUserReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	q := sqlc.New(tx)

	id := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, tx.QueryRow(ctx, query, args...).Scan(&n))
		return n
	}
	appA := id("INSERT INTO applications(key,name) VALUES('scope-reads-a','A') RETURNING id")
	appB := id("INSERT INTO applications(key,name) VALUES('scope-reads-b','B') RETURNING id")
	addUser := func(app any, subject string) int64 {
		return id("INSERT INTO users(application_id,external_id) VALUES($1,$2) RETURNING id", app, subject)
	}
	a1 := addUser(appA, "scope-reads-a1")
	b1 := addUser(appB, "scope-reads-b1")
	legacy := addUser(nil, "scope-reads-legacy")
	a2 := addUser(appA, "scope-reads-a2")
	addDevice := func(app any, user int64, token string) int64 {
		return id("INSERT INTO device_tokens(application_id,user_id,push_token,platform,provider) VALUES($1,$2,$3,'ANDROID','FCM') RETURNING id", app, user, token)
	}
	d1 := addDevice(appA, a1, "fixture-scope-a1")
	addDevice(appB, b1, "fixture-scope-b1")
	addDevice(nil, a1, "fixture-scope-unassigned")
	addDevice(appA, a2, "fixture-scope-other-user")
	d2 := addDevice(appA, a1, "fixture-scope-a2")

	row, err := q.GetScopedUser(ctx, sqlc.GetScopedUserParams{ApplicationID: appA, UserID: a1})
	require.NoError(t, err)
	require.Equal(t, "scope-reads-a1", row.ExternalID)
	for _, foreign := range []int64{b1, legacy, 0} {
		_, err := q.GetScopedUser(ctx, sqlc.GetScopedUserParams{ApplicationID: appA, UserID: foreign})
		require.ErrorIs(t, err, pgx.ErrNoRows)
	}
	users, err := q.ListScopedUsers(ctx, sqlc.ListScopedUsersParams{ApplicationID: appA, PageSize: 1})
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, a1, users[0].ID)
	users, err = q.ListScopedUsers(ctx, sqlc.ListScopedUsersParams{ApplicationID: appA, AfterID: a1, PageSize: 101})
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, a2, users[0].ID)
	for _, params := range []sqlc.ListScopedUsersParams{
		{PageSize: 10}, {ApplicationID: appA, AfterID: -1, PageSize: 10},
		{ApplicationID: appA, PageSize: -1}, {ApplicationID: appA, PageSize: 102},
	} {
		users, err := q.ListScopedUsers(ctx, params)
		require.NoError(t, err)
		require.Empty(t, users)
	}
	devices, err := q.ListScopedUserDevices(ctx, sqlc.ListScopedUserDevicesParams{ApplicationID: appA, UserID: a1, PageSize: 101})
	require.NoError(t, err)
	require.Len(t, devices, 2)
	require.Equal(t, d1, devices[0].ID)
	require.Equal(t, d2, devices[1].ID)
	devices, err = q.ListScopedUserDevices(ctx, sqlc.ListScopedUserDevicesParams{ApplicationID: appA, UserID: a1, AfterID: d1, PageSize: 1})
	require.NoError(t, err)
	require.Len(t, devices, 1)
	require.Equal(t, d2, devices[0].ID)
	devices, err = q.ListScopedUserDevices(ctx, sqlc.ListScopedUserDevicesParams{ApplicationID: appB, UserID: a1, PageSize: 101})
	require.NoError(t, err)
	require.Empty(t, devices)
	staff := id("INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Scope fixture','scope-segments@example.invalid','fixture' FROM roles WHERE name='SUPER_ADMIN' RETURNING id")
	segmentA := id("INSERT INTO segments(application_id,name,created_by) VALUES($1,'Segment A',$2) RETURNING id", appA, staff)
	segmentB := id("INSERT INTO segments(application_id,name,created_by) VALUES($1,'Segment B',$2) RETURNING id", appB, staff)
	member := id("INSERT INTO segment_members(application_id,segment_id,user_id) VALUES($1,$2,$3) RETURNING id", appA, segmentA, a1)
	id("INSERT INTO segment_members(segment_id,user_id) VALUES($1,$2) RETURNING id", segmentA, legacy)
	id("INSERT INTO segment_members(application_id,segment_id,user_id) VALUES($1,$2,$3) RETURNING id", appB, segmentB, b1)

	segments, err := q.ListScopedSegments(ctx, sqlc.ListScopedSegmentsParams{ApplicationID: appA, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, segments, 1)
	require.Equal(t, segmentA, segments[0].ID)
	_, err = q.GetScopedSegment(ctx, sqlc.GetScopedSegmentParams{ApplicationID: appA, SegmentID: segmentB})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	members, err := q.ListScopedSegmentMembers(ctx, sqlc.ListScopedSegmentMembersParams{ApplicationID: appA, SegmentID: segmentA, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, member, members[0].ID)
	require.Equal(t, a1, members[0].UserID)
	members, err = q.ListScopedSegmentMembers(ctx, sqlc.ListScopedSegmentMembersParams{ApplicationID: appA, SegmentID: segmentB, PageSize: 20})
	require.NoError(t, err)
	require.Empty(t, members)

}
