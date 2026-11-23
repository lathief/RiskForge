package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ChangeType says how an item differs between the base and the target version.
type ChangeType string

const (
	ChangeAdded    ChangeType = "ADDED"
	ChangeRemoved  ChangeType = "REMOVED"
	ChangeModified ChangeType = "MODIFIED"
)

// FieldChange is one field that differs. Before is nil when the field had no value (or the
// item was added); After is nil when it has none now (or the item was removed).
type FieldChange struct {
	Field  string
	Before *string
	After  *string
}

// ItemChange is one added, removed, or modified item, identified by Key (a rule code,
// characteristic code, matrix row order, variable code, or test case code).
type ItemChange struct {
	Key    string
	Change ChangeType
	Fields []FieldChange
	// Bins holds the bin changes of a modified scorecard characteristic, keyed by bin order.
	Bins []ItemChange
}

// Comparable is one side of a comparison: the content of a version and the dictionary it is
// pinned to.
type Comparable struct {
	DictionarySchemaVersion int64
	Dictionary              []DictionaryVariable
	Content                 Content
}

// Diff is the difference between a base version and a target version (SYSTEM_DESIGN.md 11.4):
// RAC rules, scorecard (base score, characteristics, bins, points), decision matrix rows,
// dictionary variables, and test cases. Every value is text, so a reviewer reads exactly
// what is stored. Derived formulas live in code (SYSTEM_DESIGN.md 8.2): a change to them is a
// new evaluator, not part of a policy, so it is not a diff item.
type Diff struct {
	RACRules               []ItemChange
	BaseScore              *FieldChange
	Characteristics        []ItemChange
	MatrixRows             []ItemChange
	DictionarySchema       *FieldChange
	DictionaryVariables    []ItemChange
	TestCases              []ItemChange
}

// Changed reports whether anything differs.
func (d Diff) Changed() bool {
	return len(d.RACRules)+len(d.Characteristics)+len(d.MatrixRows)+len(d.DictionaryVariables)+len(d.TestCases) > 0 ||
		d.BaseScore != nil || d.DictionarySchema != nil
}

// Counts is the number of added, removed, and modified items of one section.
type Counts struct{ Added, Removed, Modified int }

func count(items []ItemChange) Counts {
	var c Counts
	for _, i := range items {
		switch i.Change {
		case ChangeAdded:
			c.Added++
		case ChangeRemoved:
			c.Removed++
		case ChangeModified:
			c.Modified++
		}
	}
	return c
}

func (d Diff) RuleCounts() Counts           { return count(d.RACRules) }
func (d Diff) CharacteristicCounts() Counts { return count(d.Characteristics) }
func (d Diff) MatrixCounts() Counts         { return count(d.MatrixRows) }
func (d Diff) VariableCounts() Counts       { return count(d.DictionaryVariables) }
func (d Diff) TestCaseCounts() Counts       { return count(d.TestCases) }

// CompareVersions computes the diff from base to target. A nil base means there is nothing to
// compare against (the first version of a policy): everything in the target is ADDED.
func CompareVersions(base *Comparable, target Comparable) Diff {
	b := Comparable{}
	if base != nil {
		b = *base
	}
	d := Diff{
		RACRules:            diffKeyed(ruleItems(b.Content.Rules), ruleItems(target.Content.Rules)),
		MatrixRows:          diffKeyed(matrixItems(b.Content.Matrix), matrixItems(target.Content.Matrix)),
		DictionaryVariables: diffKeyed(variableItems(b.Dictionary), variableItems(target.Dictionary)),
		TestCases:           diffKeyed(caseItems(b.Content.TestCases), caseItems(target.Content.TestCases)),
	}
	var bBase, tBase *string
	if b.Content.Scorecard != nil {
		bBase = &b.Content.Scorecard.BaseScore
	}
	if target.Content.Scorecard != nil {
		tBase = &target.Content.Scorecard.BaseScore
	}
	if !sameText(bBase, tBase) {
		d.BaseScore = &FieldChange{Field: "base_score", Before: bBase, After: tBase}
	}
	d.Characteristics = diffCharacteristics(b.Content.Scorecard, target.Content.Scorecard)
	if base != nil && b.DictionarySchemaVersion != target.DictionarySchemaVersion {
		before, after := strconv.FormatInt(b.DictionarySchemaVersion, 10), strconv.FormatInt(target.DictionarySchemaVersion, 10)
		d.DictionarySchema = &FieldChange{Field: "dictionary_schema_version", Before: &before, After: &after}
	}
	return d
}

// item is one comparable item: its key and its fields in a fixed order.
type item struct {
	key    string
	fields []field
}

type field struct {
	name  string
	value *string
}

func text(s string) *string { return &s }

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// diffKeyed compares two lists of items by key. The result lists modified and removed items in
// the order of the base, then added items in the order of the target.
func diffKeyed(base, target []item) []ItemChange {
	byKey := map[string]item{}
	for _, t := range target {
		byKey[t.key] = t
	}
	inBase := map[string]bool{}
	var out []ItemChange
	for _, b := range base {
		inBase[b.key] = true
		t, ok := byKey[b.key]
		if !ok {
			out = append(out, ItemChange{Key: b.key, Change: ChangeRemoved, Fields: fieldsOf(b, false)})
			continue
		}
		if changes := compareFields(b, t); len(changes) > 0 {
			out = append(out, ItemChange{Key: b.key, Change: ChangeModified, Fields: changes})
		}
	}
	for _, t := range target {
		if !inBase[t.key] {
			out = append(out, ItemChange{Key: t.key, Change: ChangeAdded, Fields: fieldsOf(t, true)})
		}
	}
	return out
}

// fieldsOf lists the fields of an added (after) or removed (before) item.
func fieldsOf(i item, added bool) []FieldChange {
	out := make([]FieldChange, 0, len(i.fields))
	for _, f := range i.fields {
		if f.value == nil {
			continue
		}
		if added {
			out = append(out, FieldChange{Field: f.name, After: f.value})
		} else {
			out = append(out, FieldChange{Field: f.name, Before: f.value})
		}
	}
	return out
}

func compareFields(b, t item) []FieldChange {
	var out []FieldChange
	for i, f := range b.fields {
		if !sameText(f.value, t.fields[i].value) {
			out = append(out, FieldChange{Field: f.name, Before: f.value, After: t.fields[i].value})
		}
	}
	return out
}

func ruleItems(rules RuleSet) []item {
	out := make([]item, 0, len(rules))
	for _, r := range rules {
		out = append(out, item{key: r.RuleCode, fields: []field{
			{"order", text(strconv.Itoa(r.Order))}, {"expression", text(r.Expression)}, {"action", text(string(r.Action))},
			{"reason_code", text(r.ReasonCode)}, {"description", optional(r.Description)},
		}})
	}
	return out
}

// Matrix rows have no code; a row is identified by its position, which is also its score order.
func matrixItems(m DecisionMatrix) []item {
	out := make([]item, 0, len(m))
	for _, r := range m {
		out = append(out, item{key: strconv.Itoa(r.Order), fields: []field{
			{"min_score", r.MinScore}, {"max_score", r.MaxScore}, {"grade", text(r.Grade)},
			{"decision", text(string(r.Decision))}, {"label", optional(r.Label)},
		}})
	}
	return out
}

func variableItems(vars []DictionaryVariable) []item {
	out := make([]item, 0, len(vars))
	for _, v := range vars {
		var scale *string
		if v.Scale != nil {
			scale = text(strconv.Itoa(*v.Scale))
		}
		out = append(out, item{key: v.Code, fields: []field{
			{"origin", text(v.Origin)}, {"type", text(v.Type)}, {"unit", optional(v.Unit)},
			{"required", text(strconv.FormatBool(v.Required))}, {"nullable", text(strconv.FormatBool(v.Nullable))},
			{"scale", scale}, {"min_value", v.MinValue}, {"max_value", v.MaxValue},
			{"definition_version", text(strconv.Itoa(v.Version))},
		}})
	}
	return out
}

func caseItems(cases TestCases) []item {
	out := make([]item, 0, len(cases))
	for _, c := range cases {
		reasons := append([]string{}, c.ReasonCodes...)
		sort.Strings(reasons)
		out = append(out, item{key: c.Code, fields: []field{
			{"required", text(strconv.FormatBool(c.Required))}, {"input", text(canonicalInput(c.Input))},
			{"expected_outcome", text(string(c.Outcome))}, {"expected_decision", optional(string(c.Decision))},
			{"expected_grade", optional(c.Grade)}, {"expected_reason_codes", optional(strings.Join(reasons, ","))},
			{"description", optional(c.Description)},
		}})
	}
	return out
}

// canonicalInput renders a test case input with sorted keys, so key order is not a change.
func canonicalInput(raw json.RawMessage) string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

func characteristicItem(c Characteristic) item {
	return item{key: c.Code, fields: []field{
		{"order", text(strconv.Itoa(c.Order))}, {"variable", text(c.Variable)},
		{"missing_points", c.MissingPoints}, {"description", optional(c.Description)},
	}}
}

func binItems(bins []Bin) []item {
	out := make([]item, 0, len(bins))
	for _, b := range bins {
		out = append(out, item{key: strconv.Itoa(b.Order), fields: []field{
			{"min_value", b.MinValue}, {"max_value", b.MaxValue}, {"points", text(b.Points)}, {"label", optional(b.Label)},
		}})
	}
	return out
}

func diffCharacteristics(base, target *Scorecard) []ItemChange {
	var bc, tc []Characteristic
	if base != nil {
		bc = base.Characteristics
	}
	if target != nil {
		tc = target.Characteristics
	}
	byCode := map[string]Characteristic{}
	for _, c := range tc {
		byCode[c.Code] = c
	}
	inBase := map[string]bool{}
	var out []ItemChange
	for _, b := range bc {
		inBase[b.Code] = true
		t, ok := byCode[b.Code]
		if !ok {
			out = append(out, ItemChange{Key: b.Code, Change: ChangeRemoved, Fields: fieldsOf(characteristicItem(b), false),
				Bins: diffKeyed(binItems(b.Bins), nil)})
			continue
		}
		fields := compareFields(characteristicItem(b), characteristicItem(t))
		bins := diffKeyed(binItems(b.Bins), binItems(t.Bins))
		if len(fields) > 0 || len(bins) > 0 {
			out = append(out, ItemChange{Key: b.Code, Change: ChangeModified, Fields: fields, Bins: bins})
		}
	}
	for _, t := range tc {
		if !inBase[t.Code] {
			out = append(out, ItemChange{Key: t.Code, Change: ChangeAdded, Fields: fieldsOf(characteristicItem(t), true),
				Bins: diffKeyed(nil, binItems(t.Bins))})
		}
	}
	return out
}

// Summary returns the change summary for a reviewer, one readable line per change, in the
// order of the sections.
func (d Diff) Summary() []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	show := func(s *string) string {
		if s == nil {
			return "(none)"
		}
		return "`" + *s + "`"
	}
	describe := func(kind string, items []ItemChange) {
		for _, i := range items {
			switch i.Change {
			case ChangeAdded:
				add("%s %s added", kind, i.Key)
			case ChangeRemoved:
				add("%s %s removed", kind, i.Key)
			default:
				for _, f := range i.Fields {
					add("%s %s: %s %s -> %s", kind, i.Key, f.Field, show(f.Before), show(f.After))
				}
				for _, b := range i.Bins {
					switch b.Change {
					case ChangeAdded:
						add("%s %s: bin %s added", kind, i.Key, b.Key)
					case ChangeRemoved:
						add("%s %s: bin %s removed", kind, i.Key, b.Key)
					default:
						for _, f := range b.Fields {
							add("%s %s: bin %s %s %s -> %s", kind, i.Key, b.Key, f.Field, show(f.Before), show(f.After))
						}
					}
				}
			}
		}
	}
	describe("RAC rule", d.RACRules)
	if d.BaseScore != nil {
		add("Scorecard base_score %s -> %s", show(d.BaseScore.Before), show(d.BaseScore.After))
	}
	describe("Scorecard characteristic", d.Characteristics)
	describe("Decision matrix row", d.MatrixRows)
	if d.DictionarySchema != nil {
		add("Dictionary schema version %s -> %s", show(d.DictionarySchema.Before), show(d.DictionarySchema.After))
	}
	describe("Dictionary variable", d.DictionaryVariables)
	describe("Test case", d.TestCases)
	return out
}
