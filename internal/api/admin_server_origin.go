package api

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Match URL.origin for HTTP special URLs, including default ports, dot path
// segments, encoded hosts, legacy IPv4 forms and international domain names.
func serverOrigin(value string) (string, error) {
	value = Trim(value)
	if value == "" {
		return "", nil
	}
	invalid := func() (string, error) {
		return "", Fail(-400, "公开地址需为 http(s)://主机[:端口]，不能带路径或参数")
	}
	value = strings.TrimFunc(value, func(r rune) bool { return r <= 0x20 })
	value = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, value)
	i := strings.IndexByte(value, ':')
	if i < 0 {
		return invalid()
	}
	scheme := strings.ToLower(value[:i])
	if scheme != "http" && scheme != "https" {
		return invalid()
	}
	rest := value[i+1:]
	// Empty delimiters have empty URL.search and URL.hash in the mock.
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		if i != len(rest)-1 {
			return invalid()
		}
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		if i != len(rest)-1 {
			return invalid()
		}
		rest = rest[:i]
	}
	rest = strings.TrimLeft(strings.ReplaceAll(rest, `\`, "/"), "/")
	authority, path, _ := strings.Cut(rest, "/")
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		// WHATWG serializes empty userinfo away.
		if user := authority[:at]; user != "" && user != ":" {
			return invalid()
		}
		authority = authority[at+1:]
	}
	if !serverRootPath(path) {
		return invalid()
	}
	if strings.Contains(authority, "%") && !strings.HasPrefix(authority, "[") {
		domain, port, hasPort := strings.Cut(authority, ":")
		decoded, err := url.PathUnescape(domain)
		if err != nil || strings.ContainsAny(decoded, "%<>^|:#/?@[]\\") {
			return invalid()
		}
		authority = decoded
		if hasPort {
			authority += ":" + port
		}
	}
	if !serviceHostValid(authority) {
		return invalid()
	}
	u, err := url.Parse("http://" + authority)
	if err != nil {
		return invalid()
	}
	host, ok := serverOriginHost(u.Hostname())
	if !ok {
		return invalid()
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.ParseUint(port, 10, 16)
		if e != nil {
			return invalid()
		}
		port = strconv.FormatUint(n, 10)
	}
	if port != "" && !(scheme == "http" && port == "80") && !(scheme == "https" && port == "443") {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}

func serverRootPath(path string) bool {
	if path == "" {
		return true
	}
	segments := []string{}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		dot := strings.ReplaceAll(strings.ToLower(part), "%2e", ".")
		switch dot {
		case ".":
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			// One trailing empty segment is the root slash. Preserve repeated slashes.
			if part != "" || i != len(parts)-1 {
				segments = append(segments, part)
			}
		}
	}
	return len(segments) == 0
}

func serverOriginHost(host string) (string, bool) {
	if strings.Contains(host, ":") {
		ip, err := netip.ParseAddr(host)
		if err != nil || ip.Zone() != "" {
			return "", false
		}
		// netip renders IPv4-mapped IPv6 in dotted form; URL uses hexadecimal.
		if ip.Is4In6() {
			bytes := ip.As16()
			return fmt.Sprintf("[::ffff:%x:%x]", uint16(bytes[12])<<8|uint16(bytes[13]), uint16(bytes[14])<<8|uint16(bytes[15])), true
		}
		return "[" + ip.String() + "]", true
	}
	host = strings.ToLower(norm.NFKC.String(host))
	host = strings.Map(func(r rune) rune {
		switch r {
		case '\u3002':
			return '.'
		case '\u00ad', '\u034f', '\u200b', '\u2060', '\ufeff':
			return -1
		}
		return r
	}, host)
	labels := strings.Split(host, ".")
	for i, label := range labels {
		if strings.IndexFunc(label, func(r rune) bool { return r > 127 }) >= 0 {
			encoded, ok := serverPunycode(label)
			if !ok {
				return "", false
			}
			labels[i] = "xn--" + encoded
		}
	}
	host = strings.Join(labels, ".")
	if !serviceHostValid(host) {
		return "", false
	}
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	last := parts[len(parts)-1]
	if last == "" {
		return host, true
	}
	if _, err := serverIPv4Number(last); err != nil {
		return host, true
	}
	if !serviceIPv4Valid(host) {
		return "", false
	}
	var address uint64
	for i, part := range parts {
		n, _ := serverIPv4Number(part)
		if i == len(parts)-1 {
			address += n
		} else {
			address += n << uint(8*(3-i))
		}
	}
	return fmt.Sprintf("%d.%d.%d.%d", address>>24, (address>>16)&255, (address>>8)&255, address&255), true
}
func serverIPv4Number(s string) (uint64, error) {
	base := 10
	if strings.HasPrefix(s, "0x") {
		base = 16
		s = s[2:]
	} else if len(s) > 1 && s[0] == '0' {
		base = 8
		s = s[1:]
	}
	if s == "" {
		s = "0"
	}
	return strconv.ParseUint(s, base, 32)
}

// RFC 3492 encoder, used only for the non-ASCII labels of a public origin.
func serverPunycode(label string) (string, bool) {
	runes := []rune(label)
	out := []byte{}
	for _, r := range runes {
		if r < 128 {
			out = append(out, byte(r))
		}
	}
	basic := len(out)
	handled := basic
	if basic > 0 {
		out = append(out, '-')
	}
	n, delta, bias := int64(128), int64(0), int64(72)
	digit := func(d int64) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	for handled < len(runes) {
		m := int64(0x110000)
		for _, r := range runes {
			if int64(r) >= n && int64(r) < m {
				m = int64(r)
			}
		}
		delta += (m - n) * int64(handled+1)
		n = m
		if delta > 1<<31-1 {
			return "", false
		}
		for _, r := range runes {
			if int64(r) < n {
				delta++
			}
			if int64(r) != n {
				continue
			}
			q := delta
			for k := int64(36); ; k += 36 {
				threshold := max(int64(1), min(int64(26), k-bias))
				if q < threshold {
					break
				}
				out = append(out, digit(threshold+(q-threshold)%(36-threshold)))
				q = (q - threshold) / (36 - threshold)
			}
			out = append(out, digit(q))
			d := delta
			if handled == basic {
				d /= 700
			} else {
				d /= 2
			}
			d += d / int64(handled+1)
			k := int64(0)
			for d > 455 {
				d /= 35
				k += 36
			}
			bias = k + 36*d/(d+38)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return string(out), true
}
