package gitlab_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlabsdk "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

// TestPagination_HighVolumeKeyset1500Items tests keyset pagination with 1,500 items across multiple pages.
func TestPagination_HighVolumeKeyset1500Items(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	totalProjects := 1500
	perPage := 100

	for i := 1; i <= totalProjects; i++ {
		srv.State().AddProject(&gitlabsdk.Project{
			ID:                i,
			Name:              fmt.Sprintf("project-%04d", i),
			PathWithNamespace: fmt.Sprintf("group/project-%04d", i),
		})
	}

	client, err := srv.Client()
	require.NoError(t, err)

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.PerPage = perPage
	opts.Pagination.Mode = gitlab.PaginationModeKeyset

	ctx := context.Background()
	ch := gitlab.StreamProjects(ctx, client.Projects.ListProjects, gitlabsdk.ListProjectsOptions{}, opts)

	streamed, err := gitlab.CollectAll(ch)
	require.NoError(t, err)
	require.Len(t, streamed, totalProjects)

	// Verify sequential integrity and zero duplicates
	for i, p := range streamed {
		expectedID := i + 1
		assert.Equal(t, expectedID, p.ID, "item at index %d should have ID %d", i, expectedID)
		assert.Equal(t, fmt.Sprintf("project-%04d", expectedID), p.Name)
	}
}

// TestPagination_SparseNonSequentialIDs tests keyset pagination with arbitrary non-sequential and high ID values.
func TestPagination_SparseNonSequentialIDs(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	sparseIDs := []int{3, 17, 42, 108, 999, 10000, 555555, 9999999}
	for _, id := range sparseIDs {
		srv.State().AddProject(&gitlabsdk.Project{
			ID:   id,
			Name: fmt.Sprintf("project-%d", id),
		})
	}

	client, err := srv.Client()
	require.NoError(t, err)

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.PerPage = 2
	opts.Pagination.Mode = gitlab.PaginationModeKeyset

	ch := gitlab.StreamProjects(context.Background(), client.Projects.ListProjects, gitlabsdk.ListProjectsOptions{}, opts)
	streamed, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	require.Len(t, streamed, len(sparseIDs))

	for i, p := range streamed {
		assert.Equal(t, sparseIDs[i], p.ID)
	}
}

// TestPagination_SingleItemPerPageStress tests keyset streaming with per_page=1 across 50 pages.
func TestPagination_SingleItemPerPageStress(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	totalItems := 50
	for i := 1; i <= totalItems; i++ {
		srv.State().AddProject(&gitlabsdk.Project{
			ID:   i,
			Name: fmt.Sprintf("p-%d", i),
		})
	}

	client, err := srv.Client()
	require.NoError(t, err)

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.PerPage = 1
	opts.Pagination.Mode = gitlab.PaginationModeKeyset

	ch := gitlab.StreamProjects(context.Background(), client.Projects.ListProjects, gitlabsdk.ListProjectsOptions{}, opts)
	streamed, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, streamed, totalItems)
}

// TestPagination_EmptyDataset verifies stream behavior on an empty dataset.
func TestPagination_EmptyDataset(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	client, err := srv.Client()
	require.NoError(t, err)

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.Mode = gitlab.PaginationModeKeyset

	ch := gitlab.StreamProjects(context.Background(), client.Projects.ListProjects, gitlabsdk.ListProjectsOptions{}, opts)
	streamed, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Empty(t, streamed)
}

// TestPagination_ExactPageBoundary verifies keyset pagination when total count is an exact multiple of per_page.
func TestPagination_ExactPageBoundary(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	for i := 1; i <= 20; i++ {
		srv.State().AddProject(&gitlabsdk.Project{ID: i, Name: fmt.Sprintf("p-%d", i)})
	}

	client, err := srv.Client()
	require.NoError(t, err)

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.PerPage = 10 // Exactly 2 pages of 10 items
	opts.Pagination.Mode = gitlab.PaginationModeKeyset

	ch := gitlab.StreamProjects(context.Background(), client.Projects.ListProjects, gitlabsdk.ListProjectsOptions{}, opts)
	streamed, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, streamed, 20)
}

// TestPagination_ContextCancellationMidStream tests that cancelling the context stops stream immediately.
func TestPagination_ContextCancellationMidStream(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	for i := 1; i <= 500; i++ {
		srv.State().AddProject(&gitlabsdk.Project{ID: i, Name: fmt.Sprintf("p-%d", i)})
	}

	client, err := srv.Client()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.PerPage = 10
	opts.BufferSize = 1

	ch := gitlab.StreamProjects(ctx, client.Projects.ListProjects, gitlabsdk.ListProjectsOptions{}, opts)

	received := 0
	for item := range ch {
		if item.Err != nil {
			assert.ErrorIs(t, item.Err, context.Canceled)
			break
		}
		received++
		if received == 15 {
			cancel() // Cancel after receiving 15 items
		}
	}

	assert.GreaterOrEqual(t, received, 15)
	assert.Less(t, received, 500)
}

// TestPagination_InfiniteLoopDefense verifies that if the server returns non-advancing next cursor, StreamAll exits safely.
func TestPagination_InfiniteLoopDefense(t *testing.T) {
	fetchCount := int32(0)

	stalledFetcher := func(ctx context.Context, opt gitlab.PaginationOptions) ([]string, *http.Response, error) {
		atomic.AddInt32(&fetchCount, 1)
		httpResp := &http.Response{Header: make(http.Header)}
		// Malicious or buggy server returning same id_after cursor repeatedly
		httpResp.Header.Set("Link", `<http://example.com/api/v4/items?id_after=10&pagination=keyset>; rel="next"`)
		return []string{"item1"}, httpResp, nil
	}

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.Mode = gitlab.PaginationModeKeyset
	opts.Pagination.IDAfter = 10 // Starting cursor already at 10

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := gitlab.StreamAll(ctx, stalledFetcher, opts)
	results, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	// Must terminate after 1 fetch rather than looping forever
	assert.Len(t, results, 1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&fetchCount))
}

// TestPagination_MalformedLinkHeaders tests parser resilience against broken link headers.
func TestPagination_MalformedLinkHeaders(t *testing.T) {
	testCases := []struct {
		name     string
		header   string
		expected map[string]string
	}{
		{
			name:     "empty header",
			header:   "",
			expected: map[string]string{},
		},
		{
			name:     "no rel attribute",
			header:   `<http://gitlab.example.com/api/v4/projects?id_after=10>`,
			expected: map[string]string{},
		},
		{
			name:     "multiple rels with various spacing and quotes",
			header:   `<https://gl.com/p?id_after=5>; rel=next, <https://gl.com/p?page=1>; rel="first"`,
			expected: map[string]string{"next": "https://gl.com/p?id_after=5", "first": "https://gl.com/p?page=1"},
		},
		{
			name:     "broken angle brackets",
			header:   `http://gl.com/p?id_after=5; rel="next"`,
			expected: map[string]string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			links := gitlab.ParseLinkHeader(tc.header)
			assert.Equal(t, tc.expected, links)
		})
	}
}
