package managedhome

import (
	"fmt"

	"github.com/xolan/xoldot/internal/config"
)

func saveLedger(path string, records []linkRecord) error {
	data, err := encodeLedger(records)
	if err != nil {
		return err
	}
	if err := config.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save managed link state: %w", err)
	}
	return nil
}
