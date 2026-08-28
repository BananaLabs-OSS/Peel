package main

import "github.com/BananaLabs-OSS/Peel/lib/targetaddr"

func validBackendAddr(addr string) bool {
	return targetaddr.Valid(addr)
}
