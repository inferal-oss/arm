package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

func (a *app) database(args []string) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("use arm database stats, check, backup or remove; see arm database --help")
	}
	operation := args[0]
	if operation == "remove" {
		return a.databaseRemove(args[1:])
	}
	if operation != "stats" && operation != "check" && operation != "backup" {
		return 2, fmt.Errorf("unknown database operation %q; use arm database --help", operation)
	}
	flags := a.flags("database " + operation)
	flags.Bool("json", false, "structured output, also used automatically in pipes")
	full := false
	if operation == "check" {
		flags.BoolVar(&full, "full", false, "run explicit full SQLite integrity check (bounded two minutes), not only quick_check")
	}
	if err := a.parse(flags, args[1:], false); err != nil {
		return 2, err
	}
	destination := ""
	if operation == "backup" {
		var err error
		destination, err = filepath.Abs(flags.Arg(0))
		if err != nil {
			return 2, err
		}
	}
	limit := database.QuickTimeout
	if full {
		limit = database.FullTimeout
	}
	if operation == "backup" {
		limit = database.BackupTimeout
	}
	ctx, cancel := context.WithTimeout(a.ctx, limit)
	defer cancel()
	var result any
	source, err := database.Offline(ctx, a.home)
	if err == nil {
		switch operation {
		case "stats":
			result, err = database.ReadStats(ctx, source)
		case "check":
			result, err = database.CheckDatabase(ctx, source, full)
		case "backup":
			result, err = database.BackupDatabase(ctx, source, destination)
		}
		if err = errors.Join(err, source.Close()); err != nil {
			return 1, database.OperationError(ctx, operation, err)
		}
	} else if errors.Is(err, database.ErrAuthorityBusy) {
		if _, endpointErr := local.ReadEndpoint(a.home); endpointErr != nil {
			return 1, endpointErr
		}
		var raw any
		if err = client.Call(ctx, a.home, "database_"+operation, map[string]any{"destination": destination, "full": full}, &raw); err != nil {
			return 1, database.OperationError(ctx, operation, err)
		}
		result = raw
	} else {
		return 1, database.OperationError(ctx, operation, err)
	}
	if err := a.print(result); err != nil {
		return 1, err
	}
	// A completed structural check that found corruption is a useful report and
	// still a failed verification, never an implicit repair success.
	switch value := result.(type) {
	case database.Check:
		if !value.OK {
			return 1, model.Err("database_check_failed", "SQLite structural verification did not pass; inspect findings, no repair attempted")
		}
	case map[string]any:
		if operation == "check" {
			if ok, _ := value["ok"].(bool); !ok {
				return 1, model.Err("database_check_failed", "SQLite structural verification did not pass; inspect findings, no repair attempted")
			}
		}
	}
	return 0, nil
}
