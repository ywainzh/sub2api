//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release        *GitHubRelease
	recentReleases []*GitHubRelease
	recentErr      error
	lastRepo       string
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(_ context.Context, repo string) (*GitHubRelease, error) {
	s.lastRepo = repo
	return s.release, nil
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(_ context.Context, repo string, _ int) ([]*GitHubRelease, error) {
	s.lastRepo = repo
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
		},
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
}

func TestUpdateServiceChecksForkReleases(t *testing.T) {
	client := &updateServiceGitHubClientStub{
		release: &GitHubRelease{TagName: "v0.1.1"},
	}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.1.0", "release")

	info, err := svc.CheckUpdate(context.Background(), true)

	require.NoError(t, err)
	require.True(t, info.HasUpdate)
	require.Equal(t, "ywainzh/sub2api", client.lastRepo)
}

func TestUpdateServiceDetectsDeploymentMode(t *testing.T) {
	t.Setenv("SUB2API_DEPLOYMENT_MODE", "docker")
	require.Equal(t, "docker", detectDeploymentMode())

	t.Setenv("SUB2API_DEPLOYMENT_MODE", "binary")
	require.Equal(t, "binary", detectDeploymentMode())

	t.Setenv("SUB2API_DEPLOYMENT_MODE", "unsupported")
	mode := detectDeploymentMode()
	require.Contains(t, []string{"docker", "binary"}, mode)
}

func TestUpdateServiceArchiveNamesSupportBothReleaseConventions(t *testing.T) {
	names := (&UpdateService{}).getArchiveNames()
	require.Len(t, names, 2)
	require.Contains(t, names, fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH))
	require.Contains(t, names, fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH))
}

func TestUpdateServiceRejectsInPlaceChangesForDocker(t *testing.T) {
	t.Setenv("SUB2API_DEPLOYMENT_MODE", "docker")
	svc := NewUpdateService(&updateServiceCacheStub{}, &updateServiceGitHubClientStub{}, "0.1.1", "release")

	require.ErrorIs(t, svc.PerformUpdate(context.Background()), ErrDockerUpdateUnsupported)
	require.ErrorIs(t, svc.Rollback(), ErrDockerRollbackRequiresVer)
	require.ErrorIs(t, svc.RollbackToVersion(context.Background(), "0.1.0"), ErrDockerUpdateUnsupported)
}

func TestUpdateServiceSchedulesPinnedDockerImage(t *testing.T) {
	t.Setenv("SUB2API_DEPLOYMENT_MODE", "docker")
	t.Setenv("SUB2API_DEPLOY_DIR", "/opt/sub2api")
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{release: &GitHubRelease{TagName: "v0.1.2"}},
		"0.1.1",
		"release",
	)

	type commandCall struct {
		name string
		args []string
	}
	var calls []commandCall
	svc.commandRunner = func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, commandCall{name: name, args: append([]string(nil), args...)})
		return "ok", nil
	}

	outcome, err := svc.PerformUpdateOutcome(context.Background())

	require.NoError(t, err)
	require.False(t, outcome.NeedRestart)
	require.True(t, outcome.RestartScheduled)
	require.Len(t, calls, 2)
	require.Equal(t, "docker pull", calls[0].name)
	require.Equal(t, []string{"pull", "ghcr.io/ywainzh/sub2api:v0.1.2"}, calls[0].args)
	require.Equal(t, "schedule Docker update", calls[1].name)
	require.Contains(t, calls[1].args, "TARGET_IMAGE=ghcr.io/ywainzh/sub2api:v0.1.2")
	require.Contains(t, calls[1].args, "/app/docker-update-helper.sh")
}

func TestUpdateServiceRejectsNonSemverDockerRelease(t *testing.T) {
	t.Setenv("SUB2API_DEPLOYMENT_MODE", "docker")
	t.Setenv("SUB2API_DEPLOY_DIR", "/opt/sub2api")
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{release: &GitHubRelease{TagName: "v9.9.9-rc.1"}},
		"0.1.1",
		"release",
	)
	called := false
	svc.commandRunner = func(context.Context, string, ...string) (string, error) {
		called = true
		return "", nil
	}

	_, err := svc.PerformUpdateOutcome(context.Background())

	require.ErrorIs(t, err, ErrInvalidDockerReleaseTag)
	require.False(t, called)
}

func newRollbackTestService(current string, releases []*GitHubRelease) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148", PublishedAt: "2026-07-09T00:00:00Z"},                       // newer than current: excluded
		{TagName: "v0.1.147", PublishedAt: "2026-07-08T00:00:00Z"},                       // current: excluded
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "v0.1.146", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "v0.1.143", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.144", versions[1].Version)
	require.Equal(t, "0.1.143", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.145", versions[1].Version)
	require.Equal(t, "0.1.144", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.148"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148"},
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
		{TagName: "v0.1.144"},
		{TagName: "v0.1.143"},
		{TagName: "v0.1.142"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	for _, target := range []string{
		"",         // empty
		"0.1.147",  // current version
		"v0.1.147", // current version with prefix
		"0.1.148",  // newer than current
		"0.1.142",  // older than the 3 most recent
		"9.9.9",    // nonexistent
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}
