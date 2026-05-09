//go:build unit

package service

import (
	"context"
	"errors"
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
	release *GitHubRelease
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(context.Context, string) (*GitHubRelease, error) {
	return s.release, nil
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

func TestParseVersionWithSuffix(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want [3]int
	}{
		{name: "plain", in: "0.1.126", want: [3]int{0, 1, 126}},
		{name: "v prefix", in: "v0.1.126", want: [3]int{0, 1, 126}},
		{name: "E suffix", in: "0.1.126E", want: [3]int{0, 1, 126}},
		{name: "tag with E suffix", in: "v0.1.126E", want: [3]int{0, 1, 126}},
		{name: "lowercase suffix", in: "v0.1.126e", want: [3]int{0, 1, 126}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseVersion(tt.in); got != tt.want {
				t.Fatalf("parseVersion(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestCompareVersionsWithSuffix(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    int
	}{
		{name: "detects newer E release", current: "0.1.125E", latest: "0.1.126E", want: -1},
		{name: "detects newer E release from plain current", current: "0.1.125", latest: "0.1.126E", want: -1},
		{name: "suffix is newer than plain same numeric version", current: "0.1.126", latest: "0.1.126E", want: -1},
		{name: "F suffix is newer than E suffix", current: "0.1.126E", latest: "0.1.126F", want: -1},
		{name: "F suffix is older than next patch E suffix", current: "0.1.126F", latest: "0.1.127E", want: -1},
		{name: "current newer than latest", current: "0.1.127E", latest: "0.1.126E", want: 1},
		{name: "same suffix version", current: "0.1.126E", latest: "0.1.126E", want: 0},
		{name: "suffix comparison ignores case", current: "0.1.126e", latest: "0.1.126F", want: -1},
		{name: "same suffix ignores case", current: "0.1.126e", latest: "0.1.126E", want: 0},
		{name: "plain current is newer than lower suffix patch", current: "0.1.127", latest: "0.1.126F", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareVersions(tt.current, tt.latest); got != tt.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}
