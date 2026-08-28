package targetaddr

import "strings"

// Valid reports whether addr is a syntactically valid host:port target.
// It accepts IPv4, bracketed IPv6, hostnames, and the :port shorthand
// accepted by net.ResolveUDPAddr.
func Valid(addr string) bool {
	if addr == "" {
		return false
	}
	if strings.HasPrefix(addr, "[") {
		end := strings.Index(addr, "]")
		if end < 0 || end+1 >= len(addr) || addr[end+1] != ':' {
			return false
		}
		return validPort(addr[end+2:])
	}
	index := strings.LastIndex(addr, ":")
	if index < 0 || index == len(addr)-1 {
		return false
	}
	return validPort(addr[index+1:])
}

func validPort(value string) bool {
	if len(value) == 0 || len(value) > 5 {
		return false
	}
	port := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
		port = port*10 + int(char-'0')
		if port > 65535 {
			return false
		}
	}
	return port > 0
}
