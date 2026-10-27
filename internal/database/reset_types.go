package database

import (
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

const ResetDirectoryName = local.ResetHistoryName
const ResetSeedName = local.ResetSeedPointerName
const ResetSchema = 1

// ResetPlan is exact local deletion scope, never a cleanup instruction for an
// artifact or a remote machine. Ordinary removal erases three reviewed SQLite
// names. Explicit forgetting also covers the rollback journal and the two exact
// reviewed local bookkeeping trees. Retired mesh and operation receipts remain.
type ResetPlan struct {
	ForgetMount string              `json:"forget_mount,omitempty"`
	ForgetTrees []ForgetTree        `json:"forget_bookkeeping,omitempty"`
	Forget      bool                `json:"forget,omitempty"`
	Schema      int                 `json:"schema"`
	ID          string              `json:"operation"`
	Home        string              `json:"home"`
	Actor       local.Identity      `json:"actor"`
	Reason      string              `json:"reason"`
	Created     time.Time           `json:"created_at"`
	OldHost     string              `json:"old_authority"`
	NewHost     string              `json:"new_authority"`
	Proof       SettledProof        `json:"settled_proof"`
	Namespaces  []NamespaceEvidence `json:"reviewed_namespaces"`
	Files       []File              `json:"database_files"`
	Mesh        *MeshRetirement     `json:"mesh,omitempty"`
	RetireMesh  bool                `json:"retire_mesh"`
	StorageRoot *model.FileIdentity `json:"preserved_storage_root,omitempty"`
	PriorSeed   File                `json:"previous_seed_pointer"`
	Preserved   []string            `json:"preserved"`
	Effects     []string            `json:"effects"`
	SHA256      string              `json:"plan_sha256"`
}

// ResetReceipt is durable progress for the one reviewed plan. Each destructive
// action is preceded by a saved phase; recovery compares exact namespace
// identities and never guesses a replacement path from a phase string.
type ResetReceipt struct {
	ForgetMoves  map[string]string   `json:"forget_moves,omitempty"`
	ForgetErased map[string]bool     `json:"forget_erased,omitempty"`
	Schema       int                 `json:"schema"`
	Plan         ResetPlan           `json:"plan"`
	Phase        string              `json:"phase"`
	Step         string              `json:"step,omitempty"`
	Revision     int64               `json:"revision"`
	Updated      time.Time           `json:"updated_at"`
	Seed         File                `json:"fresh_database"`
	SeedReady    bool                `json:"fresh_database_ready"`
	Moves        map[string]string   `json:"database_moves"`
	Archives     map[string]string   `json:"archives"`
	Protected    []NamespaceEvidence `json:"protected_namespaces"`
	Completed    *time.Time          `json:"completed_at,omitempty"`
}

// ResetSeed references a prebuilt fresh authority, not permission to repair an
// established database. The ledger already contains its applied-operation
// marker before the inode can be installed at state.db.
type ResetSeed struct {
	Schema     int    `json:"schema"`
	Operation  string `json:"operation"`
	Authority  string `json:"authority"`
	PlanSHA256 string `json:"plan_sha256"`
	File       File   `json:"fresh_database"`
	Installed  bool   `json:"installed"`
}

type ResetResult struct {
	Operation string   `json:"operation"`
	Completed bool     `json:"completed"`
	OldHost   string   `json:"old_authority"`
	NewHost   string   `json:"new_authority"`
	Receipt   string   `json:"receipt"`
	Preserved []string `json:"preserved"`
	Detail    string   `json:"detail"`
}
