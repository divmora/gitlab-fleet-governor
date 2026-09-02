package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlabsdk "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

func TestParseLinkHeader(t *testing.T) {
	raw := `<https://gitlab.com/api/v4/projects?id_after=100&order_by=id&pagination=keyset&per_page=100&sort=asc>; rel="next", <https://gitlab.com/api/v4/projects?order_by=id&pagination=keyset&per_page=100&sort=asc>; rel="first"`
	links := gitlab.ParseLinkHeader(raw)

	assert.Equal(t, "https://gitlab.com/api/v4/projects?id_after=100&order_by=id&pagination=keyset&per_page=100&sort=asc", links["next"])
	assert.Equal(t, "https://gitlab.com/api/v4/projects?order_by=id&pagination=keyset&per_page=100&sort=asc", links["first"])
}

func TestStreamAll_KeysetPagination(t *testing.T) {
	type Project struct {
		ID   int
		Name string
	}

	fetcher := func(ctx context.Context, opt gitlab.PaginationOptions) ([]Project, *http.Response, error) {
		httpResp := &http.Response{Header: make(http.Header)}

		switch opt.IDAfter {
		case 0:
			// Page 1: IDs 1..3
			httpResp.Header.Set("Link", `<https://gitlab.com/api/v4/projects?id_after=3&pagination=keyset>; rel="next"`)
			return []Project{{ID: 1, Name: "p1"}, {ID: 2, Name: "p2"}, {ID: 3, Name: "p3"}}, httpResp, nil
		case 3:
			// Page 2: IDs 4..5 (last page)
			return []Project{{ID: 4, Name: "p4"}, {ID: 5, Name: "p5"}}, httpResp, nil
		default:
			return nil, httpResp, nil
		}
	}

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.Mode = gitlab.PaginationModeKeyset

	ch := gitlab.StreamAll(context.Background(), fetcher, opts)
	results, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, results, 5)
	assert.Equal(t, 1, results[0].ID)
	assert.Equal(t, 5, results[4].ID)
}

func TestStreamAll_PagePagination(t *testing.T) {
	type Group struct {
		ID   int
		Path string
	}

	fetcher := func(ctx context.Context, opt gitlab.PaginationOptions) ([]Group, *http.Response, error) {
		httpResp := &http.Response{Header: make(http.Header)}

		switch opt.Page {
		case 1:
			httpResp.Header.Set("X-Page", "1")
			httpResp.Header.Set("X-Next-Page", "2")
			httpResp.Header.Set("X-Total-Pages", "2")
			return []Group{{ID: 10, Path: "grp-1"}, {ID: 20, Path: "grp-2"}}, httpResp, nil
		case 2:
			httpResp.Header.Set("X-Page", "2")
			httpResp.Header.Set("X-Next-Page", "")
			httpResp.Header.Set("X-Total-Pages", "2")
			return []Group{{ID: 30, Path: "grp-3"}}, httpResp, nil
		default:
			return nil, httpResp, nil
		}
	}

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.Mode = gitlab.PaginationModePage

	ch := gitlab.StreamAll(context.Background(), fetcher, opts)
	results, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Equal(t, 10, results[0].ID)
	assert.Equal(t, 30, results[2].ID)
}

func TestStreamAll_AutoFallbackOn400(t *testing.T) {
	type Item struct {
		ID int
	}

	attempts := 0
	fetcher := func(ctx context.Context, opt gitlab.PaginationOptions) ([]Item, *http.Response, error) {
		attempts++
		if opt.Mode == gitlab.PaginationModeKeyset {
			resp := &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
			}
			return nil, resp, errors.New("keyset pagination not supported")
		}
		// Fallback to page pagination succeeds
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
		}
		resp.Header.Set("X-Page", "1")
		resp.Header.Set("X-Next-Page", "")
		return []Item{{ID: 99}}, resp, nil
	}

	opts := gitlab.DefaultStreamOptions()
	opts.Pagination.Mode = gitlab.PaginationModeKeyset
	opts.AutoFallback = true

	ch := gitlab.StreamAll(context.Background(), fetcher, opts)
	results, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 99, results[0].ID)
	assert.Equal(t, 2, attempts)
}

func TestStreamProjects_Helper(t *testing.T) {
	mockListProjects := func(opt *gitlabsdk.ListProjectsOptions, options ...gitlabsdk.RequestOptionFunc) ([]*gitlabsdk.Project, *gitlabsdk.Response, error) {
		httpResp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
		}
		if opt.IDAfter == nil || *opt.IDAfter == 0 {
			httpResp.Header.Set("Link", `<https://gitlab.com/api/v4/projects?id_after=10&pagination=keyset>; rel="next"`)
			return []*gitlabsdk.Project{{ID: 10, Name: "project-10"}}, &gitlabsdk.Response{Response: httpResp}, nil
		}
		return []*gitlabsdk.Project{{ID: 20, Name: "project-20"}}, &gitlabsdk.Response{Response: httpResp}, nil
	}

	ch := gitlab.StreamProjects(context.Background(), mockListProjects, gitlabsdk.ListProjectsOptions{}, gitlab.DefaultStreamOptions())
	results, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, 10, results[0].ID)
	assert.Equal(t, 20, results[1].ID)
}

func TestStreamGroups_Helper(t *testing.T) {
	mockListGroups := func(opt *gitlabsdk.ListGroupsOptions, options ...gitlabsdk.RequestOptionFunc) ([]*gitlabsdk.Group, *gitlabsdk.Response, error) {
		httpResp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
		}
		return []*gitlabsdk.Group{{ID: 100, Name: "group-100"}}, &gitlabsdk.Response{Response: httpResp}, nil
	}

	ch := gitlab.StreamGroups(context.Background(), mockListGroups, gitlabsdk.ListGroupsOptions{}, gitlab.DefaultStreamOptions())
	results, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 100, results[0].ID)
}

func TestStreamSubgroups_Helper(t *testing.T) {
	mockListSubgroups := func(gid any, opt *gitlabsdk.ListSubGroupsOptions, options ...gitlabsdk.RequestOptionFunc) ([]*gitlabsdk.Group, *gitlabsdk.Response, error) {
		httpResp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
		}
		return []*gitlabsdk.Group{{ID: 200, Name: "subgroup-200"}}, &gitlabsdk.Response{Response: httpResp}, nil
	}

	ch := gitlab.StreamSubgroups(context.Background(), mockListSubgroups, 100, gitlabsdk.ListSubGroupsOptions{}, gitlab.DefaultStreamOptions())
	results, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 200, results[0].ID)
}

func TestStreamGroupProjects_Helper(t *testing.T) {
	mockListGroupProjects := func(gid any, opt *gitlabsdk.ListGroupProjectsOptions, options ...gitlabsdk.RequestOptionFunc) ([]*gitlabsdk.Project, *gitlabsdk.Response, error) {
		httpResp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
		}
		return []*gitlabsdk.Project{{ID: 300, Name: "group-project-300"}}, &gitlabsdk.Response{Response: httpResp}, nil
	}

	ch := gitlab.StreamGroupProjects(context.Background(), mockListGroupProjects, 100, gitlabsdk.ListGroupProjectsOptions{}, gitlab.DefaultStreamOptions())
	results, err := gitlab.CollectAll(ch)

	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 300, results[0].ID)
}
