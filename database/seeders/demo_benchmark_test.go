package seeders

import (
	"context"
	"strconv"
	"testing"

	"github.com/lathief/RiskForge/app/modules/decision/application"
	"github.com/lathief/RiskForge/app/modules/decision/infrastructure"
)

// Engine-only benchmarks (RF-024, SYSTEM_DESIGN.md 15.3 and 16.1). They measure the policy
// evaluation of the Decision API without HTTP, authentication, JSON encoding, or PostgreSQL:
// features, RAC, scorecard, matrix, and precedence on the demo policy, with the compiled policy
// already in memory. The end-to-end numbers (HTTP and database) come from scripts/benchmark.sh.
// Both are recorded in docs/BENCHMARK.md. Run them with:
//
//	go test -run XXX -bench . -benchmem ./database/seeders/ ./app/...

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return n
}

func benchApplicant(changes ...string) map[string]any {
	applicant := map[string]any{
		"age_years": float64(29), "monthly_income": "12000000.00", "existing_installment": "2500000.00",
		"employment_tenure_months": float64(36), "slik_max_kol": float64(1), "is_blacklisted": false,
	}
	for i := 0; i+1 < len(changes); i += 2 {
		switch changes[i] {
		case "age":
			applicant["age_years"] = float64(atoi(changes[i+1]))
		case "slik":
			applicant["slik_max_kol"] = float64(atoi(changes[i+1]))
		}
	}
	return applicant
}

func resolvedDemo(b *testing.B) application.ResolvedPolicy {
	b.Helper()
	v := frozenDemoVersion(b, "11111111-1111-4111-8111-111111111111", 1, 21)
	p, err := infrastructure.NewPolicyResolver(&fakeActive{version: v}).ResolveActive(context.Background(), "KTA_DEMO", application.DefaultScope)
	if err != nil {
		b.Fatal(err)
	}
	return p
}

// BenchmarkEvaluate is one evaluation of a compiled policy, per kind of result. A hard reject is
// cheaper because the scorecard and matrix do not run.
func BenchmarkEvaluate(b *testing.B) {
	p := resolvedDemo(b)
	for name, applicant := range map[string]map[string]any{
		"approve":     benchApplicant(),
		"refer":       benchApplicant("slik", "3"),
		"hard_reject": benchApplicant("age", "19"),
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := p.Evaluate(applicant); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkEvaluateParallel runs the same compiled policy from every CPU at once, as concurrent
// requests do; it shows whether the shared immutable policy scales.
func BenchmarkEvaluateParallel(b *testing.B) {
	p := resolvedDemo(b)
	applicant := benchApplicant()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := p.Evaluate(applicant); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestBenchmarkApplicantsDecideAsNamed keeps the benchmark honest: each named input must produce
// the result its name says, otherwise the numbers would measure something else.
func TestBenchmarkApplicantsDecideAsNamed(t *testing.T) {
	v := frozenDemoVersion(t, "11111111-1111-4111-8111-111111111111", 1, 21)
	p, err := infrastructure.NewPolicyResolver(&fakeActive{version: v}).ResolveActive(context.Background(), "KTA_DEMO", application.DefaultScope)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]struct {
		applicant map[string]any
		decision  string
	}{
		"approve":     {benchApplicant(), "APPROVE"},
		"refer":       {benchApplicant("slik", "3"), "REFER"},
		"hard_reject": {benchApplicant("age", "19"), "REJECT"},
	} {
		out, err := p.Evaluate(want.applicant)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out.FinalDecision != want.decision {
			t.Errorf("%s: got %s, want %s", name, out.FinalDecision, want.decision)
		}
	}
}
