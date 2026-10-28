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
	// Check type and byte length inside SQLite, before the driver can copy the
	// value into Go. CASE excludes unsupported values; substr independently
	// bounds the copied bytes even for an altered canonical-length TEXT value.
	wantBytes := len(kubesource.ContractValue)
	var kind string
	var size sql.NullInt64
	var raw []byte
	err := tx.QueryRow(`SELECT typeof(value),length(CAST(value AS BLOB)),
CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB))=?
THEN substr(CAST(value AS BLOB),1,?) ELSE NULL END
FROM metadata WHERE key=?`, wantBytes, wantBytes+1, kubesource.ContractKey).Scan(&kind, &size, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES(?,?)`, kubesource.ContractKey, kubesource.ContractValue)
		return err
	}
	if err != nil {
		return err
	}
	if kind != "text" || !size.Valid || size.Int64 != int64(wantBytes) || len(raw) != wantBytes || string(raw) != kubesource.ContractValue {
		return fmt.Errorf("Kubernetes source contract is unsupported or noncanonical")
	}
	return nil
}
