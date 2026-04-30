package main

import (
	_ "github.com/BananaLabs-OSS/Pulp-ext-http"
	_ "github.com/BananaLabs-OSS/Pulp-ext-udp"

	"github.com/BananaLabs-OSS/Pulp/run"
)

func main() {
	run.Main()
}
