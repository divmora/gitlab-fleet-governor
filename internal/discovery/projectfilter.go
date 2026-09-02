package discovery

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// FilterDecision indicates why a project was accepted or rejected.
type FilterDecision string

const (
	DecisionMatched            FilterDecision = "matched"
	DecisionExcludedNamespace  FilterDecision = "excluded_namespace"
	DecisionExcludedRegex      FilterDecision = "excluded_regex"
	DecisionExcludedTopic      FilterDecision = "excluded_topic"
	DecisionMissingNamespace   FilterDecision = "missing_namespace"
	DecisionMissingRegex       FilterDecision = "missing_regex"
	DecisionMissingTopic       FilterDecision = "missing_topic"
	DecisionMismatchVisibility FilterDecision = "mismatch_visibility"
	DecisionMismatchArchived   FilterDecision = "mismatch_archived"
	DecisionMismatchIDRange    FilterDecision = "mismatch_id_range"
	DecisionNilProject         FilterDecision = "nil_project"
)

// FilterResult contains detailed match diagnostics for logging and dry-run reporting.
type FilterResult struct {
	Matched     bool           `json:"matched"`
	Decision    FilterDecision `json:"decision"`
	Reason      string         `json:"reason"`
	MatchedRule string         `json:"matched_rule,omitempty"`
}

// ProjectFilter executes a multi-criteria filtering pipeline against GitLab projects.
type ProjectFilter struct {
	namespacesInclude []string
	namespacesExclude []string
	regexInclude      *regexp.Regexp
	regexExclude      *regexp.Regexp
	topicsInclude     map[string]struct{}
	topicsExclude     map[string]struct{}
	visibility        string
	archived          *bool
	idRange           *config.IDRange
}

// NewProjectFilter compiles and initializes a ProjectFilter from configuration.
func NewProjectFilter(sel *config.ProjectSelector) (*ProjectFilter, error) {
	if sel == nil {
		return &ProjectFilter{}, nil
	}

	pf := &ProjectFilter{
		visibility: strings.ToLower(strings.TrimSpace(sel.Visibility)),
		archived:   sel.Archived,
		idRange:    sel.IDRange,
	}

	// 1. Normalize namespaces
	for _, ns := range sel.NamespacesInclude {
		trimmed := strings.Trim(strings.ToLower(ns), "/")
		if trimmed != "" {
			pf.namespacesInclude = append(pf.namespacesInclude, trimmed)
		}
	}
	for _, ns := range sel.NamespacesExclude {
		trimmed := strings.Trim(strings.ToLower(ns), "/")
		if trimmed != "" {
			pf.namespacesExclude = append(pf.namespacesExclude, trimmed)
		}
	}

	// 2. Precompile regular expressions
	if strings.TrimSpace(sel.ProjectNameRegexInclude) != "" {
		re, err := regexp.Compile(sel.ProjectNameRegexInclude)
		if err != nil {
			return nil, fmt.Errorf("invalid project_name_regex_include '%s': %w", sel.ProjectNameRegexInclude, err)
		}
		pf.regexInclude = re
	}
	if strings.TrimSpace(sel.ProjectNameRegexExclude) != "" {
		re, err := regexp.Compile(sel.ProjectNameRegexExclude)
		if err != nil {
			return nil, fmt.Errorf("invalid project_name_regex_exclude '%s': %w", sel.ProjectNameRegexExclude, err)
		}
		pf.regexExclude = re
	}

	// 3. Normalize topics into O(1) lookup sets
	if len(sel.TopicsInclude) > 0 {
		pf.topicsInclude = make(map[string]struct{}, len(sel.TopicsInclude))
		for _, topic := range sel.TopicsInclude {
			t := strings.TrimSpace(strings.ToLower(topic))
			if t != "" {
				pf.topicsInclude[t] = struct{}{}
			}
		}
	}
	if len(sel.TopicsExclude) > 0 {
		pf.topicsExclude = make(map[string]struct{}, len(sel.TopicsExclude))
		for _, topic := range sel.TopicsExclude {
			t := strings.TrimSpace(strings.ToLower(topic))
			if t != "" {
				pf.topicsExclude[t] = struct{}{}
			}
		}
	}

	return pf, nil
}

// Matches returns true if the project passes all filter criteria.
func (pf *ProjectFilter) Matches(p *gitlab.Project) bool {
	res := pf.Evaluate(p)
	return res.Matched
}

// Evaluate runs the multi-criteria pipeline and returns comprehensive diagnostics.
func (pf *ProjectFilter) Evaluate(p *gitlab.Project) FilterResult {
	if p == nil {
		return FilterResult{
			Matched:  false,
			Decision: DecisionNilProject,
			Reason:   "project is nil",
		}
	}

	if pf == nil {
		return FilterResult{
			Matched:  true,
			Decision: DecisionMatched,
			Reason:   "no filter configured (matches all)",
		}
	}

	// Step 1: Archived Status Check
	if pf.archived != nil {
		if p.Archived != *pf.archived {
			return FilterResult{
				Matched:  false,
				Decision: DecisionMismatchArchived,
				Reason:   fmt.Sprintf("project archived status (%t) does not match required (%t)", p.Archived, *pf.archived),
			}
		}
	}

	// Step 2: Visibility Check
	if pf.visibility != "" && pf.visibility != "any" {
		projVis := strings.ToLower(string(p.Visibility))
		if projVis != pf.visibility {
			return FilterResult{
				Matched:  false,
				Decision: DecisionMismatchVisibility,
				Reason:   fmt.Sprintf("project visibility '%s' does not match required '%s'", projVis, pf.visibility),
			}
		}
	}

	// Step 3: ID Range Check
	if pf.idRange != nil {
		if pf.idRange.Min > 0 && p.ID < pf.idRange.Min {
			return FilterResult{
				Matched:  false,
				Decision: DecisionMismatchIDRange,
				Reason:   fmt.Sprintf("project ID %d is below minimum ID %d", p.ID, pf.idRange.Min),
			}
		}
		if pf.idRange.Max > 0 && p.ID > pf.idRange.Max {
			return FilterResult{
				Matched:  false,
				Decision: DecisionMismatchIDRange,
				Reason:   fmt.Sprintf("project ID %d is above maximum ID %d", p.ID, pf.idRange.Max),
			}
		}
	}

	// Step 4: Namespace Exclude Check (Precedence)
	projNS := extractProjectNamespace(p)
	if len(pf.namespacesExclude) > 0 {
		for _, excludeNS := range pf.namespacesExclude {
			if matchNamespace(projNS, excludeNS) {
				return FilterResult{
					Matched:     false,
					Decision:    DecisionExcludedNamespace,
					Reason:      fmt.Sprintf("project namespace '%s' matched excluded namespace '%s'", projNS, excludeNS),
					MatchedRule: excludeNS,
				}
			}
		}
	}

	// Step 5: Project Name / Path Regex Exclude Check (Precedence)
	if pf.regexExclude != nil {
		if matchProjectRegex(pf.regexExclude, p) {
			return FilterResult{
				Matched:     false,
				Decision:    DecisionExcludedRegex,
				Reason:      fmt.Sprintf("project name/path '%s' matched excluded regex '%s'", p.Name, pf.regexExclude.String()),
				MatchedRule: pf.regexExclude.String(),
			}
		}
	}

	// Step 6: Topics Exclude Check (Precedence)
	projectTopics := extractProjectTopics(p)
	if len(pf.topicsExclude) > 0 {
		for _, pt := range projectTopics {
			if _, excluded := pf.topicsExclude[pt]; excluded {
				return FilterResult{
					Matched:     false,
					Decision:    DecisionExcludedTopic,
					Reason:      fmt.Sprintf("project contains excluded topic '%s'", pt),
					MatchedRule: pt,
				}
			}
		}
	}

	// Step 7: Namespace Include Check
	if len(pf.namespacesInclude) > 0 {
		matched := false
		var matchedRule string
		for _, incNS := range pf.namespacesInclude {
			if matchNamespace(projNS, incNS) {
				matched = true
				matchedRule = incNS
				break
			}
		}
		if !matched {
			return FilterResult{
				Matched:  false,
				Decision: DecisionMissingNamespace,
				Reason:   fmt.Sprintf("project namespace '%s' did not match any included namespaces", projNS),
			}
		}
		_ = matchedRule
	}

	// Step 8: Project Name / Path Regex Include Check
	if pf.regexInclude != nil {
		if !matchProjectRegex(pf.regexInclude, p) {
			return FilterResult{
				Matched:  false,
				Decision: DecisionMissingRegex,
				Reason:   fmt.Sprintf("project name/path '%s' did not match included regex '%s'", p.Name, pf.regexInclude.String()),
			}
		}
	}

	// Step 9: Topics Include Check
	if len(pf.topicsInclude) > 0 {
		matched := false
		for _, pt := range projectTopics {
			if _, ok := pf.topicsInclude[pt]; ok {
				matched = true
				break
			}
		}
		if !matched {
			return FilterResult{
				Matched:  false,
				Decision: DecisionMissingTopic,
				Reason:   "project does not contain any of the required included topics",
			}
		}
	}

	return FilterResult{
		Matched:  true,
		Decision: DecisionMatched,
		Reason:   "passed all filter criteria",
	}
}

func extractProjectNamespace(p *gitlab.Project) string {
	if p == nil {
		return ""
	}
	if p.Namespace != nil && p.Namespace.FullPath != "" {
		return strings.Trim(strings.ToLower(p.Namespace.FullPath), "/")
	}
	if p.PathWithNamespace != "" {
		idx := strings.LastIndex(p.PathWithNamespace, "/")
		if idx != -1 {
			return strings.Trim(strings.ToLower(p.PathWithNamespace[:idx]), "/")
		}
	}
	return ""
}

func extractProjectTopics(p *gitlab.Project) []string {
	if p == nil {
		return nil
	}
	var topics []string
	seen := make(map[string]bool)

	add := func(list []string) {
		for _, item := range list {
			t := strings.TrimSpace(strings.ToLower(item))
			if t != "" && !seen[t] {
				seen[t] = true
				topics = append(topics, t)
			}
		}
	}

	add(p.Topics)
	add(p.TagList)
	return topics
}

func matchNamespace(projNS, targetNS string) bool {
	projNS = strings.Trim(strings.ToLower(projNS), "/")
	targetNS = strings.Trim(strings.ToLower(targetNS), "/")
	if projNS == "" || targetNS == "" {
		return projNS == targetNS
	}
	if projNS == targetNS {
		return true
	}
	return strings.HasPrefix(projNS, targetNS+"/")
}

func matchProjectRegex(re *regexp.Regexp, p *gitlab.Project) bool {
	if re == nil || p == nil {
		return false
	}
	candidates := []string{
		p.Name,
		strings.ToLower(p.Name),
		p.Path,
		strings.ToLower(p.Path),
		p.PathWithNamespace,
		strings.ToLower(p.PathWithNamespace),
		strings.ReplaceAll(strings.ToLower(p.Name), " - ", "-"),
		strings.ReplaceAll(strings.ToLower(p.Name), " ", "-"),
	}
	for _, c := range candidates {
		if c != "" && re.MatchString(c) {
			return true
		}
	}
	return false
}
