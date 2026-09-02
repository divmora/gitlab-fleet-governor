package config

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

const maxEnvSubstDepth = 32

// EnvLookupFunc is a function signature matching os.LookupEnv for variable resolution.
type EnvLookupFunc func(key string) (string, bool)

// ExpandEnv replaces ${VAR} and ${VAR:-default} tokens in s using os.LookupEnv.
func ExpandEnv(s string) (string, error) {
	return ExpandEnvWithLookup(s, os.LookupEnv)
}

// ExpandEnvWithLookup replaces tokens using the provided lookup function.
func ExpandEnvWithLookup(s string, lookup EnvLookupFunc) (string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return expandEnvRecursive(s, lookup, 0)
}

func expandEnvRecursive(s string, lookup EnvLookupFunc, depth int) (string, error) {
	if depth > maxEnvSubstDepth {
		return "", fmt.Errorf("env substitution error: maximum recursion depth (%d) exceeded", maxEnvSubstDepth)
	}

	var sb strings.Builder
	runes := []rune(s)
	n := len(runes)
	i := 0

	for i < n {
		// Handle backslash escape: \$ -> $
		if runes[i] == '\\' && i+1 < n && runes[i+1] == '$' {
			sb.WriteRune('$')
			i += 2
			continue
		}

		if runes[i] == '$' {
			// Handle double dollar escape: $$ -> $
			if i+1 < n && runes[i+1] == '$' {
				sb.WriteRune('$')
				i += 2
				continue
			}

			// Handle braced expression: ${...}
			if i+1 < n && runes[i+1] == '{' {
				start := i
				i += 2 // skip ${
				exprDepth := 1
				exprStart := i
				exprEnd := -1

				for i < n {
					if runes[i] == '$' && i+1 < n && runes[i+1] == '{' {
						exprDepth++
						i += 2
						continue
					}
					if runes[i] == '}' {
						exprDepth--
						if exprDepth == 0 {
							exprEnd = i
							i++ // skip closing }
							break
						}
					}
					i++
				}

				if exprEnd == -1 {
					return "", fmt.Errorf("syntax error in env substitution: unterminated '${' at character offset %d", start)
				}

				expr := string(runes[exprStart:exprEnd])
				val, err := evaluateExpression(expr, lookup, depth+1)
				if err != nil {
					return "", fmt.Errorf("error expanding '${%s}' at offset %d: %w", expr, start, err)
				}
				sb.WriteString(val)
				continue
			}

			// Single $ not followed by { or $ is a literal $ (preserves regexes ending in $)
			sb.WriteRune('$')
			i++
			continue
		}

		sb.WriteRune(runes[i])
		i++
	}

	return sb.String(), nil
}

func evaluateExpression(expr string, lookup EnvLookupFunc, depth int) (string, error) {
	// Locate top-level :- delimiter
	colonIdx := -1
	runes := []rune(expr)
	n := len(runes)
	braceDepth := 0

	for i := 0; i < n; i++ {
		if runes[i] == '$' && i+1 < n && runes[i+1] == '{' {
			braceDepth++
			i++
			continue
		}
		if runes[i] == '}' {
			if braceDepth > 0 {
				braceDepth--
			}
			continue
		}
		if braceDepth == 0 && runes[i] == ':' && i+1 < n && runes[i+1] == '-' {
			colonIdx = i
			break
		}
	}

	if colonIdx != -1 {
		varName := strings.TrimSpace(string(runes[:colonIdx]))
		defaultExpr := string(runes[colonIdx+2:])

		if !isValidVarIdentifier(varName) {
			return "", fmt.Errorf("invalid environment variable name '%s'", varName)
		}

		val, exists := lookup(varName)
		// POSIX :- semantics: use default if unset OR empty
		if exists && val != "" {
			return expandEnvRecursive(val, lookup, depth+1)
		}

		// Recursively evaluate the default expression
		return expandEnvRecursive(defaultExpr, lookup, depth)
	}

	// Simple ${VAR}
	varName := strings.TrimSpace(expr)
	if !isValidVarIdentifier(varName) {
		return "", fmt.Errorf("invalid environment variable name '%s'", varName)
	}

	val, exists := lookup(varName)
	if exists && val != "" {
		return expandEnvRecursive(val, lookup, depth+1)
	}
	return val, nil
}

func isValidVarIdentifier(name string) bool {
	if len(name) == 0 {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}
