package gitlab

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// PaginationMode defines keyset vs page-based pagination.
type PaginationMode string

const (
	PaginationModeKeyset PaginationMode = "keyset"
	PaginationModePage   PaginationMode = "page"
)

// PaginationOptions provides configuration parameters for paginated queries.
type PaginationOptions struct {
	Mode      PaginationMode `yaml:"mode" json:"mode"`
	Page      int            `yaml:"page" json:"page"`
	PerPage   int            `yaml:"per_page" json:"per_page"`
	OrderBy   string         `yaml:"order_by" json:"order_by"`
	Sort      string         `yaml:"sort" json:"sort"`
	IDAfter   int            `yaml:"id_after" json:"id_after"`
	PageToken string         `yaml:"page_token" json:"page_token"`
}

// DefaultPaginationOptions returns sensible defaults for high-volume endpoints.
func DefaultPaginationOptions() PaginationOptions {
	return PaginationOptions{
		Mode:    PaginationModeKeyset,
		Page:    1,
		PerPage: 100,
		OrderBy: "id",
		Sort:    "asc",
	}
}

// PageResponse contains metadata parsed from standard and keyset pagination headers.
type PageResponse struct {
	CurrentPage int
	PerPage     int
	NextPage    int
	PrevPage    int
	Total       int
	TotalPages  int
	NextURL     string
	NextIDAfter int
	HasNext     bool
}

// StreamItem wraps a streamed resource item and any error encountered.
type StreamItem[T any] struct {
	Value T
	Err   error
}

// StreamOptions configures stream buffering and fallback behaviors.
type StreamOptions struct {
	Pagination   PaginationOptions
	BufferSize   int
	AutoFallback bool
}

// DefaultStreamOptions returns production stream defaults.
func DefaultStreamOptions() StreamOptions {
	return StreamOptions{
		Pagination:   DefaultPaginationOptions(),
		BufferSize:   100,
		AutoFallback: true,
	}
}

var linkRegex = regexp.MustCompile(`<([^>]+)>;\s*rel="?([^";]+)"?`)

// ParseLinkHeader parses RFC 5988 Link headers returned by GitLab API.
// Example: `<https://gitlab.com/api/v4/projects?id_after=42&pagination=keyset>; rel="next"`
func ParseLinkHeader(header string) map[string]string {
	links := make(map[string]string)
	if header == "" {
		return links
	}

	parts := strings.Split(header, ",")
	for _, part := range parts {
		matches := linkRegex.FindStringSubmatch(strings.TrimSpace(part))
		if len(matches) == 3 {
			targetURL := matches[1]
			rel := strings.ToLower(matches[2])
			links[rel] = targetURL
		}
	}
	return links
}

// ParsePageResponse parses all GitLab pagination headers from an HTTP response.
func ParsePageResponse(resp *http.Response) PageResponse {
	var pr PageResponse
	if resp == nil {
		return pr
	}

	h := resp.Header

	// Parse Page-based headers
	if v, err := strconv.Atoi(h.Get("X-Page")); err == nil {
		pr.CurrentPage = v
	}
	if v, err := strconv.Atoi(h.Get("X-Per-Page")); err == nil {
		pr.PerPage = v
	}
	if v, err := strconv.Atoi(h.Get("X-Next-Page")); err == nil && v > 0 {
		pr.NextPage = v
		pr.HasNext = true
	}
	if v, err := strconv.Atoi(h.Get("X-Prev-Page")); err == nil {
		pr.PrevPage = v
	}
	if v, err := strconv.Atoi(h.Get("X-Total")); err == nil {
		pr.Total = v
	}
	if v, err := strconv.Atoi(h.Get("X-Total-Pages")); err == nil {
		pr.TotalPages = v
	}

	// Parse Keyset Link header
	linkHeader := h.Get("Link")
	if linkHeader != "" {
		links := ParseLinkHeader(linkHeader)
		if nextURL, ok := links["next"]; ok && nextURL != "" {
			pr.NextURL = nextURL
			pr.HasNext = true

			// Parse query parameters from nextURL
			if parsed, err := url.Parse(nextURL); err == nil {
				q := parsed.Query()
				if idAfterStr := q.Get("id_after"); idAfterStr != "" {
					if idAfter, err := strconv.Atoi(idAfterStr); err == nil {
						pr.NextIDAfter = idAfter
					}
				}
				if pageStr := q.Get("page"); pageStr != "" && pr.NextPage == 0 {
					if p, err := strconv.Atoi(pageStr); err == nil {
						pr.NextPage = p
					}
				}
			}
		}
	}

	return pr
}

// PageFetcherFunc is a generic function that fetches a single page of items.
type PageFetcherFunc[T any] func(ctx context.Context, opt PaginationOptions) ([]T, *http.Response, error)

// StreamAll executes generic keyset or page pagination and streams items through a channel.
func StreamAll[T any](ctx context.Context, fetcher PageFetcherFunc[T], opts StreamOptions) <-chan StreamItem[T] {
	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = 100
	}
	out := make(chan StreamItem[T], bufSize)

	go func() {
		defer close(out)

		currentOpt := opts.Pagination
		if currentOpt.PerPage <= 0 {
			currentOpt.PerPage = 100
		}
		if currentOpt.Page <= 0 {
			currentOpt.Page = 1
		}

		for {
			select {
			case <-ctx.Done():
				out <- StreamItem[T]{Err: ctx.Err()}
				return
			default:
			}

			items, resp, err := fetcher(ctx, currentOpt)
			if err != nil {
				// Check for keyset fallback condition: 400 Bad Request when keyset is requested
				if opts.AutoFallback && currentOpt.Mode == PaginationModeKeyset && resp != nil && resp.StatusCode == http.StatusBadRequest {
					currentOpt.Mode = PaginationModePage
					currentOpt.Page = 1
					currentOpt.IDAfter = 0
					continue
				}
				out <- StreamItem[T]{Err: err}
				return
			}

			// Stream items to channel
			for _, item := range items {
				select {
				case <-ctx.Done():
					out <- StreamItem[T]{Err: ctx.Err()}
					return
				case out <- StreamItem[T]{Value: item}:
				}
			}

			// Parse response headers to check for termination
			pageMeta := ParsePageResponse(resp)

			// Termination checks:
			if len(items) == 0 {
				return
			}

			if currentOpt.Mode == PaginationModeKeyset {
				if !pageMeta.HasNext || pageMeta.NextURL == "" {
					return
				}
				if pageMeta.NextIDAfter > 0 {
					if pageMeta.NextIDAfter <= currentOpt.IDAfter {
						// Guard against infinite loop
						return
					}
					currentOpt.IDAfter = pageMeta.NextIDAfter
				} else {
					return
				}
			} else {
				// Page-based pagination
				if !pageMeta.HasNext || pageMeta.NextPage <= currentOpt.Page {
					return
				}
				currentOpt.Page = pageMeta.NextPage
			}
		}
	}()

	return out
}

// CollectAll drains a StreamItem channel into a slice, returning the first error if encountered.
func CollectAll[T any](ch <-chan StreamItem[T]) ([]T, error) {
	var results []T
	for item := range ch {
		if item.Err != nil {
			return results, item.Err
		}
		results = append(results, item.Value)
	}
	return results, nil
}

// ============================================================================
// Domain-Specific Streamers for Fleet Discovery & Governance
// ============================================================================

// StreamProjects streams projects matching criteria using keyset pagination.
func StreamProjects(
	ctx context.Context,
	listFn func(opt *gitlab.ListProjectsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Project, *gitlab.Response, error),
	baseOpt gitlab.ListProjectsOptions,
	opts StreamOptions,
) <-chan StreamItem[*gitlab.Project] {
	fetcher := func(ctx context.Context, pOpt PaginationOptions) ([]*gitlab.Project, *http.Response, error) {
		reqOpt := baseOpt
		reqOpt.PerPage = pOpt.PerPage
		if pOpt.Mode == PaginationModeKeyset {
			reqOpt.Pagination = "keyset"
			if pOpt.OrderBy != "" {
				reqOpt.OrderBy = gitlab.Ptr(pOpt.OrderBy)
			}
			if pOpt.Sort != "" {
				reqOpt.Sort = gitlab.Ptr(pOpt.Sort)
			}
			if pOpt.IDAfter > 0 {
				reqOpt.IDAfter = gitlab.Ptr(pOpt.IDAfter)
			}
		} else {
			reqOpt.Page = pOpt.Page
		}

		projects, resp, err := listFn(&reqOpt, gitlab.WithContext(ctx))
		var httpResp *http.Response
		if resp != nil {
			httpResp = resp.Response
		}
		return projects, httpResp, err
	}

	return StreamAll(ctx, fetcher, opts)
}

// StreamGroups streams top-level groups matching criteria using keyset pagination.
func StreamGroups(
	ctx context.Context,
	listFn func(opt *gitlab.ListGroupsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Group, *gitlab.Response, error),
	baseOpt gitlab.ListGroupsOptions,
	opts StreamOptions,
) <-chan StreamItem[*gitlab.Group] {
	fetcher := func(ctx context.Context, pOpt PaginationOptions) ([]*gitlab.Group, *http.Response, error) {
		reqOpt := baseOpt
		reqOpt.PerPage = pOpt.PerPage
		if pOpt.Mode == PaginationModeKeyset {
			reqOpt.Pagination = "keyset"
			if pOpt.OrderBy != "" {
				reqOpt.OrderBy = gitlab.Ptr(pOpt.OrderBy)
			}
			if pOpt.Sort != "" {
				reqOpt.Sort = gitlab.Ptr(pOpt.Sort)
			}
			if pOpt.PageToken != "" {
				reqOpt.PageToken = pOpt.PageToken
			} else if pOpt.IDAfter > 0 {
				reqOpt.PageToken = strconv.Itoa(pOpt.IDAfter)
			}
		} else {
			reqOpt.Page = pOpt.Page
		}

		groups, resp, err := listFn(&reqOpt, gitlab.WithContext(ctx))
		var httpResp *http.Response
		if resp != nil {
			httpResp = resp.Response
		}
		return groups, httpResp, err
	}

	return StreamAll(ctx, fetcher, opts)
}

// StreamSubgroups streams subgroups of a parent group.
func StreamSubgroups(
	ctx context.Context,
	listFn func(gid any, opt *gitlab.ListSubGroupsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Group, *gitlab.Response, error),
	groupID any,
	baseOpt gitlab.ListSubGroupsOptions,
	opts StreamOptions,
) <-chan StreamItem[*gitlab.Group] {
	fetcher := func(ctx context.Context, pOpt PaginationOptions) ([]*gitlab.Group, *http.Response, error) {
		reqOpt := baseOpt
		reqOpt.PerPage = pOpt.PerPage
		if pOpt.Mode == PaginationModeKeyset {
			reqOpt.Pagination = "keyset"
			if pOpt.OrderBy != "" {
				reqOpt.OrderBy = gitlab.Ptr(pOpt.OrderBy)
			}
			if pOpt.Sort != "" {
				reqOpt.Sort = gitlab.Ptr(pOpt.Sort)
			}
			if pOpt.PageToken != "" {
				reqOpt.PageToken = pOpt.PageToken
			} else if pOpt.IDAfter > 0 {
				reqOpt.PageToken = strconv.Itoa(pOpt.IDAfter)
			}
		} else {
			reqOpt.Page = pOpt.Page
		}

		groups, resp, err := listFn(groupID, &reqOpt, gitlab.WithContext(ctx))
		var httpResp *http.Response
		if resp != nil {
			httpResp = resp.Response
		}
		return groups, httpResp, err
	}

	return StreamAll(ctx, fetcher, opts)
}

// StreamGroupProjects streams projects within a group.
func StreamGroupProjects(
	ctx context.Context,
	listFn func(gid any, opt *gitlab.ListGroupProjectsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Project, *gitlab.Response, error),
	groupID any,
	baseOpt gitlab.ListGroupProjectsOptions,
	opts StreamOptions,
) <-chan StreamItem[*gitlab.Project] {
	fetcher := func(ctx context.Context, pOpt PaginationOptions) ([]*gitlab.Project, *http.Response, error) {
		reqOpt := baseOpt
		reqOpt.PerPage = pOpt.PerPage
		if pOpt.Mode == PaginationModeKeyset {
			reqOpt.Pagination = "keyset"
			if pOpt.OrderBy != "" {
				reqOpt.OrderBy = gitlab.Ptr(pOpt.OrderBy)
			}
			if pOpt.Sort != "" {
				reqOpt.Sort = gitlab.Ptr(pOpt.Sort)
			}
			if pOpt.PageToken != "" {
				reqOpt.PageToken = pOpt.PageToken
			} else if pOpt.IDAfter > 0 {
				reqOpt.PageToken = strconv.Itoa(pOpt.IDAfter)
			}
		} else {
			reqOpt.Page = pOpt.Page
		}

		projects, resp, err := listFn(groupID, &reqOpt, gitlab.WithContext(ctx))
		var httpResp *http.Response
		if resp != nil {
			httpResp = resp.Response
		}
		return projects, httpResp, err
	}

	return StreamAll(ctx, fetcher, opts)
}
