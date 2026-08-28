package main

import (
	"database/sql"
	"fmt"

	"github.com/BananaLabs-OSS/Fiber/pulp"
	_ "github.com/BananaLabs-OSS/Fiber/pulp/sql"

	"routing-state-cell/owner"
)

func main() {}

func init() {
	pulp.OnInit(func([]byte) error {
		db, err := sql.Open("pulp", "")
		if err != nil {
			return fmt.Errorf("open routing state database: %w", err)
		}
		state, err := owner.Open(db)
		if err != nil {
			_ = db.Close()
			return err
		}
		for name, provider := range state.Providers() {
			pulp.Provide(name, pulp.Provider(provider))
		}
		return nil
	})
}
