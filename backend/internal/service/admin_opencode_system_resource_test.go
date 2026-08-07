package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type openCodeSystemResourceRepoStub struct {
	OpenCodeProxyPoolRepository
	accountIDs map[int64]bool
	groupIDs   map[int64]bool
}

func (s *openCodeSystemResourceRepoStub) IsOpenCodeSystemWorkerAccount(_ context.Context, id int64) (bool, error) {
	return s.accountIDs[id], nil
}

func (s *openCodeSystemResourceRepoStub) IsOpenCodeSystemGroup(_ context.Context, id int64) (bool, error) {
	return s.groupIDs[id], nil
}

func TestAdminServiceRejectsOpenCodeSystemResourceMutations(t *testing.T) {
	pool := &OpenCodeProxyPoolService{repo: &openCodeSystemResourceRepoStub{
		accountIDs: map[int64]bool{26: true},
		groupIDs:   map[int64]bool{9: true},
	}}
	admin := &adminServiceImpl{openCodeProxyPool: pool}

	require.ErrorIs(t, admin.rejectOpenCodeSystemAccountMutation(context.Background(), 26), ErrOpenCodeSystemResource)
	require.NoError(t, admin.rejectOpenCodeSystemAccountMutation(context.Background(), 27))
	require.ErrorIs(t, admin.rejectOpenCodeSystemGroupMutation(context.Background(), 9), ErrOpenCodeSystemResource)
	require.NoError(t, admin.rejectOpenCodeSystemGroupMutation(context.Background(), 5))
}
