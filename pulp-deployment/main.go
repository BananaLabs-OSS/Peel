package main

import (
	_ "github.com/BananaLabs-OSS/Pulp-ext-entropy"
	_ "github.com/BananaLabs-OSS/Pulp-ext-http"
	_ "github.com/BananaLabs-OSS/Pulp-ext-sqlite"
	_ "github.com/BananaLabs-OSS/Pulp-ext-tcp"
	_ "github.com/BananaLabs-OSS/Pulp-ext-udp"

	"github.com/BananaLabs-OSS/Pulp/run"
)

func main() {
	run.Main()
}
