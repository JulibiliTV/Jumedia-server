package api

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

var serviceAddresses = [...]struct{ key, label, field string }{
	{"serviceApi", "主站接口", "api"},
	{"serviceLive", "直播接口", "live"},
	{"serviceApp", "客户端接口", "app"},
	{"servicePassport", "登录接口", "passport"},
}

type serviceCDN struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Validate without serializing the URL: mock serviceURL returns the trimmed
// input spelling, not URL.href. WHATWG permits paths with spaces/invalid percent
// escapes, backslashes, and missing authority slashes for special schemes.
func serviceURL(value, label string) (string, error) {
	value = Trim(value)
	if value == "" {
		return "", nil
	}
	invalid := func() (string, error) { return "", Fail(-400, label+"地址格式不正确") }
	parsed := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
	parsed = strings.TrimFunc(parsed, func(r rune) bool { return r <= 0x20 })
	colon := strings.IndexByte(parsed, ':')
	if colon < 1 {
		return invalid()
	}
	for i, c := range parsed[:colon] {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && ((c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'))) {
			return invalid()
		}
	}
	scheme := strings.ToLower(parsed[:colon])
	rest := parsed[colon+1:]
	restricted := false
	if scheme == "http" || scheme == "https" || scheme == "ftp" || scheme == "ws" || scheme == "wss" {
		// Split before normalizing backslashes: query/fragment contents are opaque.
		if i := strings.IndexByte(rest, '#'); i >= 0 {
			restricted = rest[i+1:] != ""
			rest = rest[:i]
		}
		if i := strings.IndexByte(rest, '?'); i >= 0 {
			restricted = restricted || rest[i+1:] != ""
			rest = rest[:i]
		}
		rest = strings.ReplaceAll(rest, `\`, "/")
		authority := strings.SplitN(strings.TrimLeft(rest, "/"), "/", 2)[0]
		if i := strings.LastIndexByte(authority, '@'); i >= 0 {
			// An empty userinfo (including just ':') has no username or password.
			restricted = restricted || strings.TrimPrefix(authority[:i], ":") != ""
			authority = authority[i+1:]
		}
		if !serviceHostValid(authority) {
			return invalid()
		}
	} else {
		// Non-special schemes are rejected by protocol after syntactic parsing.
		if _, err := url.Parse(parsed); err != nil {
			return invalid()
		}
	}
	if (scheme != "http" && scheme != "https") || restricted {
		return "", Fail(-400, label+"地址需为 http(s) 地址，不能带参数")
	}
	return strings.TrimRight(value, "/"), nil
}

func serviceHostValid(authority string) bool {
	u, err := url.Parse("//" + authority)
	// Percent-encoded domains are accepted by browsers; decode before validation.
	if strings.Contains(authority, "%") && !strings.HasPrefix(authority, "[") {
		host, port := authority, ""
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host, port = host[:i], host[i:]
		}
		decoded, e := url.PathUnescape(host)
		if e != nil || strings.ContainsAny(decoded, "%<>^|:#/?@[]\\") {
			return false
		}
		u, err = url.Parse("//" + decoded + port)
	}
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		ip, e := netip.ParseAddr(host)
		if e != nil || !ip.Is6() || ip.Zone() != "" {
			return false
		}
	} else {
		if strings.ContainsAny(host, "%<>^|:#/?@[]\\") {
			return false
		}
		for _, r := range host {
			if r <= 0x20 || r == 0x7f {
				return false
			}
		}
		if !serviceIPv4Valid(host) {
			return false
		}
	}
	if port := u.Port(); port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return false
		}
	}
	return true
}

// WHATWG treats a domain ending in a number as IPv4, including shortened,
// octal and hexadecimal forms. A failed numeric address is not a DNS name.
func serviceIPv4Valid(host string) bool {
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	number := func(s string) (uint64, error) {
		base := 10
		if strings.HasPrefix(strings.ToLower(s), "0x") {
			base = 16
			s = s[2:]
		} else if len(s) > 1 && s[0] == '0' {
			base = 8
			s = s[1:]
		}
		if s == "" {
			s = "0"
		}
		return strconv.ParseUint(s, base, 64)
	}
	last := parts[len(parts)-1]
	digits := last != ""
	for _, r := range last {
		if r < '0' || r > '9' {
			digits = false
		}
	}
	_, err := number(last)
	if !digits && (last == "" || err != nil) {
		return true
	}
	if len(parts) > 4 {
		return false
	}
	for i, part := range parts {
		if part == "" {
			return false
		}
		n, err := number(part)
		if err != nil {
			return false
		}
		limit := uint64(256)
		if i == len(parts)-1 {
			limit = uint64(1) << (8 * (5 - len(parts)))
		}
		if n >= limit {
			return false
		}
	}
	return true
}

// JS String with a nullish empty-string fallback for serviceInput values.
func serviceString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case map[string]any:
		return "[object Object]"
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = serviceString(x)
		}
		return strings.Join(parts, ",")
	case float64:
		// encoding/json follows ECMAScript's fixed/exponent thresholds.
		raw, _ := json.Marshal(v)
		return string(raw)
	default:
		return adminJSONString(v)
	}
}

func serviceInput(params map[string]string) (map[string]string, error) {
	var out map[string]string
	put := func(k, v string) {
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	for _, address := range serviceAddresses {
		if raw, ok := params[address.key]; ok {
			value, err := serviceURL(raw, address.label)
			if err != nil {
				return nil, err
			}
			put(address.key, value)
		}
	}
	if raw, ok := params["serviceCdns"]; ok {
		if raw == "" {
			raw = "[]"
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, Fail(-400, "CDN 集合格式不正确")
		}
		list, ok := value.([]any)
		if !ok {
			return nil, Fail(-400, "CDN 集合格式不正确")
		}
		if len(list) > 20 {
			return nil, Fail(-400, "CDN 集合最多 20 个")
		}
		cdns := make([]serviceCDN, 0, len(list))
		for _, value := range list {
			item, _ := value.(map[string]any)
			address, err := serviceURL(serviceString(item["url"]), "CDN ")
			if err != nil {
				return nil, err
			}
			cdns = append(cdns, serviceCDN{Name: Trim(serviceString(item["name"])), URL: address})
		}
		// Validate all URLs first, then required fields, then duplicates (mock order).
		for _, cdn := range cdns {
			if cdn.Name == "" || utf8.RuneCountInString(cdn.Name) > 20 || cdn.URL == "" {
				return nil, Fail(-400, "每个 CDN 需要填写 1–20 字的名称和地址")
			}
		}
		seen := map[string]bool{}
		for _, cdn := range cdns {
			if seen[cdn.URL] {
				return nil, Fail(-400, "CDN 地址不能重复")
			}
			seen[cdn.URL] = true
		}
		raw, err := serviceJSON(cdns)
		if err != nil {
			return nil, err
		}
		put("serviceCdns", raw)
	}
	return out, nil
}

func (s *Server) jumediaService(c *Ctx) (any, error) {
	settings, err := s.Store.Settings(c.Context())
	if err != nil {
		return nil, err
	}
	data := map[string]any{}
	for _, address := range serviceAddresses {
		value := serviceString(settings[address.key])
		if value == "" {
			value = s.Origin(c.R)
		}
		data[address.field] = value
	}
	raw := serviceString(settings["serviceCdns"])
	if raw == "" {
		raw = "[]"
	}
	var cdns any
	if err := json.Unmarshal([]byte(raw), &cdns); err != nil {
		return nil, err
	}
	data["cdn"] = cdns
	data["mtime"] = RawNumber(serviceString(settings["serviceMtime"]))
	// The shared response renderer rewrites every /upload, /media or /mock
	// string. CDN display names are arbitrary text, so emit the same envelope
	// locally to preserve them without changing that shared behavior.
	body, err := json.Marshal(map[string]any{"code": 0, "message": "0", "ttl": 1, "data": data})
	if err != nil {
		return nil, err
	}
	return Text(string(body), 200, "application/json; charset=utf-8"), nil
}

// Match JSON.stringify for HTML characters and literal Unicode line separators.
// Skip escaped backslashes so a name containing the text "\\u2028" stays intact.
func serviceJSON(value any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return "", err
	}
	raw := strings.TrimSuffix(b.String(), "\n")
	var out strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] == '\\' && i+1 < len(raw) {
			if strings.HasPrefix(raw[i:], `\u2028`) {
				out.WriteRune('\u2028')
				i += 6
				continue
			}
			if strings.HasPrefix(raw[i:], `\u2029`) {
				out.WriteRune('\u2029')
				i += 6
				continue
			}
			out.WriteString(raw[i : i+2])
			i += 2
			continue
		}
		out.WriteByte(raw[i])
		i++
	}
	return out.String(), nil
}
