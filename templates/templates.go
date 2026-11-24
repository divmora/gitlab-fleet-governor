// Package templates embeds the compliance starter policies so that
// `gitlab-fleet-governor init` works without a copy of the repository.
package templates

import (
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

//go:embed README.md */policy.yaml */README.md
var files embed.FS

// Template describes one starter policy.
type Template struct {
	Name      string
	Framework string
}

// catalog lists the templates in display order.
var catalog = []Template{
	{Name: "soc2", Framework: "SOC 2 Trust Services Criteria"},
	{Name: "iso-27001", Framework: "ISO/IEC 27001:2022 Annex A"},
	{Name: "pci-dss", Framework: "PCI DSS v4.0"},
	{Name: "hipaa", Framework: "HIPAA Security Rule"},
	{Name: "nist-800-53", Framework: "NIST SP 800-53 Rev. 5"},
	{Name: "fedramp", Framework: "FedRAMP Moderate (NIST SP 800-53 Rev. 5)"},
}

// Placeholders that init can fill in.
const (
	PlaceholderGroup         = "REPLACE_WITH_GROUP_PATH"
	PlaceholderEmailDomain   = "REPLACE_WITH_EMAIL_DOMAIN"
	PlaceholderReviewerGroup = "REPLACE_WITH_REVIEWER_GROUP"
)

var placeholderRegex = regexp.MustCompile(`REPLACE_WITH_[A-Z_]+`)

// List returns the available templates.
func List() []Template {
	return append([]Template(nil), catalog...)
}

func find(name string) (Template, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, t := range catalog {
		if t.Name == name {
			return t, nil
		}
	}
	names := make([]string, len(catalog))
	for i, t := range catalog {
		names[i] = t.Name
	}
	return Template{}, fmt.Errorf("unknown template %q (have %s)", name, strings.Join(names, ", "))
}

// Policy returns a template's policy.yaml with its placeholders in place.
func Policy(name string) ([]byte, error) {
	t, err := find(name)
	if err != nil {
		return nil, err
	}
	return files.ReadFile(t.Name + "/policy.yaml")
}

// Readme returns a template's README (overview and control mapping).
func Readme(name string) ([]byte, error) {
	t, err := find(name)
	if err != nil {
		return nil, err
	}
	return files.ReadFile(t.Name + "/README.md")
}

var (
	groupPathRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(/[A-Za-z0-9][A-Za-z0-9_.-]*)*$`)
	domainRegex    = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// Validate rejects values that aren't a GitLab group path or a domain
// name. The values are inserted into quoted YAML strings, so anything else
// (quotes, newlines, spaces) could change the policy's structure.
func (v Values) Validate() error {
	for _, g := range []struct{ flag, value string }{{"--group", v.Group}, {"--reviewer-group", v.ReviewerGroup}} {
		if val := strings.Trim(g.value, "/ "); val != "" && !groupPathRegex.MatchString(val) {
			return fmt.Errorf("%s %q is not a GitLab group path such as acme/platform", g.flag, g.value)
		}
	}
	if d := strings.TrimPrefix(strings.TrimSpace(v.EmailDomain), "@"); d != "" && !domainRegex.MatchString(d) {
		return fmt.Errorf("--email-domain %q is not a domain name such as acme.com", v.EmailDomain)
	}
	return nil
}

// Values fills the template placeholders. Empty values are left in place.
type Values struct {
	Group         string
	EmailDomain   string
	ReviewerGroup string
}

// Render fills the placeholders in policy and returns the result together
// with the placeholders that are still unfilled. The email domain is
// regex-escaped, because it is used inside push_rules.author_email_regex.
func Render(policy []byte, v Values) ([]byte, []string) {
	out := string(policy)
	replace := func(placeholder, value string) {
		if value = strings.TrimSpace(value); value != "" {
			out = strings.ReplaceAll(out, placeholder, value)
		}
	}
	replace(PlaceholderGroup, strings.Trim(v.Group, "/ "))
	replace(PlaceholderReviewerGroup, strings.Trim(v.ReviewerGroup, "/ "))
	replace(PlaceholderEmailDomain, regexp.QuoteMeta(strings.TrimPrefix(strings.TrimSpace(v.EmailDomain), "@")))

	seen := map[string]bool{}
	var missing []string
	for _, m := range placeholderRegex.FindAllString(out, -1) {
		if !seen[m] {
			seen[m] = true
			missing = append(missing, m)
		}
	}
	sort.Strings(missing)
	return []byte(out), missing
}
