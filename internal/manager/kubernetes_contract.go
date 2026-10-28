package manager

import (
	"database/sql"
	"errors"
	"fmt"
	"getarm.org/internal/kubesource"
)

// The known-new TEXT record commits before a first new source role. Unknown
// acknowledgments/refusals grant no subsequent insertion or native effects.
func ensureKubernetesSourceContractTx(tx *sql.Tx, scope kubesource.Scope) error {
	if !scope.RequiresContract() {
		return nil
	}
	var kind, raw string
	err := tx.QueryRow(`SELECT typeof(value),value FROM metadata WHERE key=?`, kubesource.ContractKey).Scan(&kind, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES(?,?)`, kubesource.ContractKey, kubesource.ContractValue)
		return err
	}
	if err != nil {
		return err
	}
	if kind != "text" || raw != kubesource.ContractValue {
		return fmt.Errorf("Kubernetes source contract is unsupported or noncanonical")
	}
	return nil
}
