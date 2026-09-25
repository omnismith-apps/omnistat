package systemd

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// envHeader opens the stored settings file (FR-013).
const envHeader = `# Settings of the omnistat service, written by ` + "`omnistat service install`" + `.
# Readable by root only: it holds the access token. systemd passes these to the
# service as its environment. Install rewrites this file, keeping each setting
# it is not given again. Restart the service after editing it by hand.
`

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// FormatEnv renders settings as a systemd environment file, sorted, one per
// line. Values are single-quoted (literal to systemd), or double-quoted with
// \ " $ ` escaped when they contain a single quote. A value systemd cannot
// hold on one line is refused; errors name the setting, never its value.
func FormatEnv(env map[string]string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(envHeader)
	for _, k := range slices.Sorted(maps.Keys(env)) {
		v := env[k]
		if !envName.MatchString(k) {
			return nil, fmt.Errorf("setting %q: not a valid environment variable name", k)
		}
		if strings.ContainsAny(v, "\n\r\x00") {
			return nil, fmt.Errorf("setting %s: its value contains a line break or NUL", k)
		}
		if !strings.Contains(v, "'") {
			fmt.Fprintf(&b, "%s='%s'\n", k, v)
			continue
		}
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`")
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, r.Replace(v))
	}
	return []byte(b.String()), nil
}

// ParseEnv reads a systemd environment file: comments (# or ;), blank lines,
// and KEY=value with unquoted, single-quoted and double-quoted parts, as
// systemd reads them. A line it cannot read is an error that names the line
// number only: the content may be a secret.
func ParseEnv(data []byte) (map[string]string, error) {
	env := map[string]string{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimLeft(strings.TrimSuffix(line, "\r"), " \t")
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		k, v, err := parseAssignment(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		env[k] = v
	}
	return env, nil
}

func parseAssignment(line string) (string, string, error) {
	k, raw, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", errors.New("not a KEY=value assignment")
	}
	k = strings.TrimSpace(k)
	if !envName.MatchString(k) {
		return "", "", errors.New("not a valid variable name")
	}
	v, err := parseValue(strings.TrimLeft(raw, " \t"))
	return k, v, err
}

// parseValue follows systemd (checked against 259): a quote opens only at
// the start of the value or right after a closing quote (blanks in between are
// skipped), so parts concatenate; after any other character quotes are
// literal. Single quotes are literal; in double quotes a backslash escapes
// " \ ` $ and is kept before anything else; outside quotes a backslash makes
// the next character literal, and trailing blanks are dropped.
func parseValue(s string) (string, error) {
	var out []byte
	keep := 0      // length of out that trailing-blank stripping must not cut
	quotes := true // a quote here opens a quoted part
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quotes && (c == ' ' || c == '\t'):
			continue
		case quotes && c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return "", errors.New("unterminated single quote")
			}
			out = append(out, s[i+1:i+1+end]...)
			i += end + 1
		case quotes && c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					if strings.IndexByte("\"\\`$", s[i+1]) < 0 {
						out = append(out, '\\')
					}
					i++
				}
				out = append(out, s[i])
			}
			if i >= len(s) {
				return "", errors.New("unterminated double quote")
			}
		case c == '\\':
			if i+1 >= len(s) {
				return "", errors.New("line continuation is not supported")
			}
			i++
			out = append(out, s[i])
			quotes = false
		default:
			out = append(out, c)
			quotes = false
			if c == ' ' || c == '\t' {
				continue
			}
		}
		keep = len(out)
	}
	return string(out[:keep]), nil
}
