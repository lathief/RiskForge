package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// SnapshotFormat is the version of the snapshot layout. It changes when a section is added
// or its meaning changes, so a checksum is only comparable between equal formats.
const SnapshotFormat = 1

// EvaluatorVersion names the expression language and evaluation rules the snapshot assumes
// (SYSTEM_DESIGN.md 9.1). The evaluator arrives in M3; the value is recorded now so every
// frozen version states which semantics it was written for.
const EvaluatorVersion = "1"

// DictionaryVariable is one dictionary definition as frozen into a snapshot. The policy
// module owns this copy so that it does not depend on the dictionary's domain types.
type DictionaryVariable struct {
	Code          string  `json:"code"`
	Origin        string  `json:"origin"`
	Type          string  `json:"type"`
	Description   string  `json:"description"`
	Unit          string  `json:"unit"`
	Required      bool    `json:"required"`
	Nullable      bool    `json:"nullable"`
	Scale         *int    `json:"scale"`
	MinValue      *string `json:"min_value"`
	MaxValue      *string `json:"max_value"`
	Version       int     `json:"version"`
	SchemaVersion int64   `json:"schema_version"`
}

type snapshotPackage struct {
	ProductCode string `json:"product_code"`
	Scope       string `json:"scope"`
}

type snapshotDictionary struct {
	SchemaVersion int64                `json:"schema_version"`
	Variables     []DictionaryVariable `json:"variables"`
}

// snapshotDocument is the frozen content of a version. RF-007 fills rac_rules (in evaluation
// order); the scorecard and decision matrix stay empty until RF-008 and RF-009. All sections are
// always present so the layout is stable and a reader never has to guess whether one was omitted.
// Rules were empty in snapshots frozen before RF-007; they remain valid under format 1.
type snapshotDocument struct {
	Format           int                `json:"format"`
	EvaluatorVersion string             `json:"evaluator_version"`
	Package          snapshotPackage    `json:"package"`
	VersionNumber    int                `json:"version_number"`
	Dictionary       snapshotDictionary `json:"dictionary"`
	RACRules         []snapshotRule     `json:"rac_rules"`
	Scorecard        any                `json:"scorecard"`
	DecisionMatrix   []any              `json:"decision_matrix"`
}

// BuildSnapshot freezes v, its RAC rules, and the dictionary it is written against into canonical JSON and
// returns it with its SHA-256 checksum. The same inputs always give the same bytes.
func BuildSnapshot(v Version, dictionary []DictionaryVariable, rules RuleSet) (canonical []byte, checksum string, err error) {
	vars := append([]DictionaryVariable{}, dictionary...)
	sort.Slice(vars, func(i, j int) bool { return vars[i].Code < vars[j].Code })
	doc := snapshotDocument{
		Format:           SnapshotFormat,
		EvaluatorVersion: EvaluatorVersion,
		Package:          snapshotPackage{ProductCode: v.ProductCode, Scope: v.Scope},
		VersionNumber:    v.Number,
		Dictionary:       snapshotDictionary{SchemaVersion: v.DictionarySchemaVersion, Variables: vars},
		RACRules:         rules.snapshot(),
		DecisionMatrix:   []any{},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	canonical, err = Canonicalize(raw)
	if err != nil {
		return nil, "", err
	}
	return canonical, Checksum(canonical), nil
}

// Canonicalize rewrites JSON with object keys sorted and no insignificant whitespace.
// PostgreSQL stores JSONB with its own key order, so the checksum is always taken over, and
// verified against, this form rather than over whatever the database returns.
func Canonicalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep numbers exactly as written
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}
	return json.Marshal(v) // maps are marshalled with sorted keys
}

// Checksum is the hex SHA-256 of canonical snapshot bytes.
func Checksum(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// VerifySnapshot reports whether stored JSON still matches its checksum.
func VerifySnapshot(stored []byte, checksum string) bool {
	canonical, err := Canonicalize(stored)
	if err != nil {
		return false
	}
	return Checksum(canonical) == checksum
}
